package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Event struct {
	ID        int64
	TaskID    sql.NullInt64
	SessionID sql.NullInt64
	Type      string
	Message   string
	ActorType sql.NullString
	ActorID   sql.NullString
	Model     sql.NullString
	RoleID    sql.NullInt64
	CreatedAt string
	PrevHash  sql.NullString
	Hash      sql.NullString
}

var ValidEventTypes = map[string]bool{
	"note": true, "decision": true, "bug": true,
	"commit": true, "blocker": true, "status_change": true,
	"decision_recorded": true, "memory_recorded": true,
	"feature_status_change": true, "archived": true,
	"approval": true, "override": true, "check": true,
	"spec_recorded": true, "dependency_added": true,
	"autonomy_promoted": true, "eval_recorded": true,
	"policy_violation": true, "guard_denied": true, "role_assigned": true,
	"row_seal": true, "seal_watermark": true, "dispatch": true,
	"spec_revised": true, "risk_changed": true, "autonomy_changed": true,
	"project_added": true, "hash_version": true, "tool_used": true, "task_updated": true, "task_deferred": true, "criterion_added": true, "criterion_checked": true, "link_added": true, "check_runner_set": true,
	"plan_proposed": true, "plan_edited": true, "plan_approved": true, "plan_rejected": true,
	headSealEvent: true, legacyAttestedEvent: true,
}

// UserLogTypes are the event types a user or agent may record directly via
// `acline log` or the MCP acline_log tool. Everything else in
// ValidEventTypes (approval, override, guard_denied, ...) is written only by
// the command that performs that action, so a direct log can't forge one.
var UserLogTypes = map[string]bool{
	"note": true, "decision": true, "bug": true, "commit": true, "blocker": true,
}

// eventHash covers the row's content plus the previous row's hash, so any edit
// or removal breaks the chain from that point on. The id is deliberately
// excluded — it is assigned by SQLite after the hash is computed, and ordering
// is already carried by prev_hash.
func eventHash(prevHash string, taskID, sessionID *int64, eventType, message, actorType, actorID, model, createdAt string) string {
	idStr := func(p *int64) string {
		if p == nil {
			return "-"
		}
		return strconv.FormatInt(*p, 10)
	}
	// Length-prefix each field so no combination of values can be re-split
	// into a different but identically-hashing sequence.
	var b strings.Builder
	for _, f := range []string{prevHash, idStr(taskID), idStr(sessionID), eventType, message, actorType, actorID, model, createdAt} {
		fmt.Fprintf(&b, "%d:%s|", len(f), f)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// eventHashV2 is eventHash that also covers the row's role_id. Events written
// after the chain's hash_version marker (see ensureEventHashV2) use it; earlier
// ones keep eventHash, so their stored hashes still verify.
func eventHashV2(prevHash string, taskID, sessionID *int64, eventType, message, actorType, actorID, model, createdAt, role string) string {
	return digest("event.v2", eventHash(prevHash, taskID, sessionID, eventType, message, actorType, actorID, model, createdAt), role)
}

// The hash_version marker is the point in the chain from which events are
// hashed with eventHashV2. It is itself an event (hashed the old way), so it
// cannot be removed or moved without breaking the chain, and a row after it
// cannot be passed off as an old-format one.
const (
	hashVersionEventType = "hash_version"
	hashVersionV2Message = "event_hash_v2: role_id is covered from the next event on"
)

// ensureEventHashV2 writes the hash_version marker inside tx when the chain has
// none yet, so the event the caller writes next is hashed with eventHashV2.
func (s *Store) ensureEventHashV2(tx *sql.Tx, sessionID *int64) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE type = ?`, hashVersionEventType).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var prevHash string
	err := tx.QueryRow(`SELECT COALESCE(hash, '') FROM events ORDER BY id DESC LIMIT 1`).Scan(&prevHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	h := eventHash(prevHash, nil, sessionID, hashVersionEventType, hashVersionV2Message, s.Actor.Type, s.Actor.ID, s.Actor.Model, now)
	_, err = tx.Exec(
		`INSERT INTO events (task_id, session_id, type, message, actor_type, actor_id, model, created_at, prev_hash, hash)
		 VALUES (NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(sessionID), hashVersionEventType, hashVersionV2Message, s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), now, prevHash, h,
	)
	return err
}

// LogEventGlobal records a history event not tied to a specific task,
// attached to the current session if one is active. Errors are swallowed:
// these calls are secondary side effects of another command's main work
// (memory_recorded, secret_redacted, embedding_failed, etc.). Shared by the
// CLI (cmd/log.go's logEventGlobal delegates here) and the MCP server
// (internal/mcp) so both log secondary events the same way.
func (s *Store) LogEventGlobal(eventType, message string) {
	var sessionID *int64
	if sess, err := s.CurrentSession(); err == nil {
		sessionID = &sess.ID
	}
	if _, err := s.LogEvent(nil, sessionID, eventType, message); err != nil {
		s.auditFailed(eventType, err)
	}
}

// LogTaskEvent is LogEventGlobal's task-scoped counterpart: a history event
// attached to a specific task (and the current session, if one is active).
// Errors are swallowed for the same reason — a secondary side effect of
// another command's main work (status_change, override, etc). Shared by
// the CLI (cmd/task.go's logTaskEvent delegates here) and the MCP server.
func (s *Store) LogTaskEvent(taskID int64, eventType, message string) {
	var sessionID *int64
	if sess, err := s.CurrentSession(); err == nil {
		sessionID = &sess.ID
	}
	if _, err := s.LogEvent(&taskID, sessionID, eventType, message); err != nil {
		s.auditFailed(eventType, err)
	}
}

// auditFailed reports a secondary audit event that could not be written. These
// callers must not fail their primary operation over it, but dropping the
// error silently meant an audit trail could lose entries with nobody the wiser.
// It goes to AuditErrorHandler when set, else stderr (for `mcp serve` that is
// the stream the VS Code extension captures in its output channel).
func (s *Store) auditFailed(eventType string, err error) {
	if s.AuditErrorHandler != nil {
		s.AuditErrorHandler(eventType, err)
		return
	}
	fmt.Fprintf(os.Stderr, "acline: warning: audit event %q was not recorded: %v\n", eventType, err)
}

// LogEvent appends a history entry. The events table is append-only and
// hash-chained, so this is the only way rows get there.
func (s *Store) LogEvent(taskID *int64, sessionID *int64, eventType, message string) (int64, error) {
	return s.logEvent(taskID, sessionID, nil, eventType, message)
}

// LogEventWithRole is LogEvent plus an explicit role_id (see Store.ResolveRole),
// for the user-facing commands roles can be attached to (`acline log`,
// `note add`'s promotion path). role_id is covered by eventHashV2, which
// applies only after the chain's hash_version marker, so rows hashed before
// it (without role_id) still verify.
func (s *Store) LogEventWithRole(taskID, sessionID, roleID *int64, eventType, message string) (int64, error) {
	return s.logEvent(taskID, sessionID, roleID, eventType, message)
}

func (s *Store) logEvent(taskID *int64, sessionID *int64, roleID *int64, eventType, message string) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, err := s.logEventTx(tx, taskID, sessionID, roleID, eventType, message)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// logEventTx appends one hash-chained event inside the caller's transaction,
// so a multi-step operation (e.g. CompleteTask) can make its state change and
// its audit trail commit or fail together.
func (s *Store) logEventTx(tx *sql.Tx, taskID *int64, sessionID *int64, roleID *int64, eventType, message string) (int64, error) {
	if eventType == "" {
		eventType = "note"
	}
	message = scrubText(message) // before hashing: the chain must cover what is stored
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.ensureEventHashV2(tx, sessionID); err != nil {
		return 0, err
	}

	var prevHash string
	err := tx.QueryRow(`SELECT COALESCE(hash, '') FROM events ORDER BY id DESC LIMIT 1`).Scan(&prevHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	h := eventHashV2(prevHash, taskID, sessionID, eventType, message,
		s.Actor.Type, s.Actor.ID, s.Actor.Model, now, roleStr(roleID))

	res, err := tx.Exec(
		`INSERT INTO events (task_id, session_id, type, message, actor_type, actor_id, model, role_id, created_at, prev_hash, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(taskID), nullInt(sessionID), eventType, message,
		s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), nullInt(roleID), now, prevHash, h,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// changeWithEvent runs one UPDATE and its audit event in a single transaction,
// so a status change and the record of it commit or fail together. kind and id
// name the row for the not-found error; the UPDATE must affect exactly it.
func (s *Store) changeWithEvent(kind string, id int64, eventType, message, query string, args ...any) error {
	return s.changeTaskWithEvent(kind, id, nil, eventType, message, query, args...)
}

// changeTaskWithEvent is changeWithEvent with the event attached to a task.
func (s *Store) changeTaskWithEvent(kind string, id int64, taskID *int64, eventType, message, query string, args ...any) error {
	sessionID := s.currentSessionID()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	if err := mustExist(res, kind, id); err != nil {
		return err
	}
	if _, err := s.logEventTx(tx, taskID, sessionID, nil, eventType, message); err != nil {
		return err
	}
	return tx.Commit()
}

// ChainResult reports the integrity of the event chain.
type ChainResult struct {
	Checked  int
	BadID    int64  // first event whose hash does not verify (0 = none)
	Reason   string // why it failed
	Unhashed int    // rows predating hashing
	// PreMarker is the events hashed before the hash_version marker (their
	// role_id is not covered), unhashed ones included.
	PreMarker int
	// Resealed means a person attested the legacy region (Store.Reseal), so it
	// was checked against that attestation rather than tolerated.
	Resealed bool
}

// Legacy reports whether the chain has records verify can only tolerate.
func (c ChainResult) Legacy() bool { return c.PreMarker > 0 && !c.Resealed }

func (c ChainResult) OK() bool { return c.BadID == 0 }

// VerifyChain recomputes every event hash in order and confirms each row links
// to its predecessor. Detects edits, deletions, and insertions made outside
// the CLI — including direct writes to the database file.
func (s *Store) VerifyChain() (ChainResult, error) {
	r, err := verifyChain(s.DB)
	if err != nil || !r.OK() {
		return r, err
	}
	id, reason, err := checkLegacyAttestation(s.DB)
	if err != nil {
		return r, err
	}
	r.Resealed = id != 0
	if reason != "" {
		r.BadID, r.Reason = id, reason
	}
	return r, nil
}

// chainQuerier is satisfied by both *sql.DB and *sql.Tx, so the chain can be
// verified inside an open transaction (LoadSnapshot needs that: with the pool
// pinned to one connection, a query on s.DB would deadlock behind the tx).
type chainQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func verifyChain(q chainQuerier) (ChainResult, error) {
	var r ChainResult
	rows, err := q.Query(`SELECT id, task_id, session_id, type, message,
		COALESCE(actor_type,''), COALESCE(actor_id,''), COALESCE(model,''), created_at,
		COALESCE(prev_hash,''), COALESCE(hash,''), CAST(COALESCE(role_id,'') AS TEXT) FROM events ORDER BY id`)
	if err != nil {
		return r, err
	}
	defer rows.Close()

	prevHash := ""
	v2 := false // set once the hash_version marker has been verified
	for rows.Next() {
		var id int64
		var taskID, sessionID sql.NullInt64
		var eventType, message, actorType, actorID, model, createdAt, storedPrev, storedHash, role string
		if err := rows.Scan(&id, &taskID, &sessionID, &eventType, &message,
			&actorType, &actorID, &model, &createdAt, &storedPrev, &storedHash, &role); err != nil {
			return r, err
		}
		r.Checked++

		if storedHash == "" {
			// Rows written before hashing existed form a leading run. An unhashed
			// row after a hashed one was blanked: its content is then unprotected.
			if prevHash != "" {
				r.BadID, r.Reason = id, "unhashed event after hashing began: its hash was removed, so its content could have been altered"
				return r, nil
			}
			r.Unhashed++
			r.PreMarker++
			continue
		}
		if storedPrev != prevHash {
			r.BadID, r.Reason = id, "chain broken: prev_hash does not match the preceding event (a row was altered or removed)"
			return r, nil
		}
		var tp, sp *int64
		if taskID.Valid {
			tp = &taskID.Int64
		}
		if sessionID.Valid {
			sp = &sessionID.Int64
		}
		want := eventHash(storedPrev, tp, sp, eventType, message, actorType, actorID, model, createdAt)
		if v2 {
			want = eventHashV2(storedPrev, tp, sp, eventType, message, actorType, actorID, model, createdAt, role)
		}
		if want != storedHash {
			r.BadID, r.Reason = id, "content hash mismatch: this event's fields were altered"
			return r, nil
		}
		if eventType == hashVersionEventType && message == hashVersionV2Message {
			v2 = true
		} else if !v2 {
			r.PreMarker++
		}
		prevHash = storedHash
	}
	return r, rows.Err()
}

type EventFilter struct {
	TaskID *int64
	// ProjectID keeps the events of the project's tasks and sessions (events
	// carry no project of their own).
	ProjectID *int64
	ActorType string
	Since     string
	Limit     int
}

func (s *Store) ListEvents(taskID *int64, limit int) ([]Event, error) {
	return s.QueryEvents(EventFilter{TaskID: taskID, Limit: limit})
}

func (s *Store) QueryEvents(f EventFilter) ([]Event, error) {
	q := `SELECT id, task_id, session_id, type, message, actor_type, actor_id, model, role_id, created_at, prev_hash, hash FROM events`
	var where []string
	var args []any
	if f.TaskID != nil {
		where = append(where, `task_id = ?`)
		args = append(args, *f.TaskID)
	}
	if f.ProjectID != nil {
		where = append(where, `(task_id IN (SELECT id FROM tasks WHERE project_id = ?) OR session_id IN (SELECT id FROM sessions WHERE project_id = ?))`)
		args = append(args, *f.ProjectID, *f.ProjectID)
	}
	if f.ActorType != "" {
		where = append(where, `actor_type = ?`)
		args = append(args, f.ActorType)
	}
	if f.Since != "" {
		where = append(where, `created_at >= ?`)
		args = append(args, f.Since)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	if f.Limit <= 0 {
		f.Limit = 20
	}
	args = append(args, f.Limit)

	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TaskID, &e.SessionID, &e.Type, &e.Message,
			&e.ActorType, &e.ActorID, &e.Model, &e.RoleID, &e.CreatedAt, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// LatestEventID is the id of the newest event, or 0 when there are none. Paired
// with CountEventsAfter it lets a caller ask "did anything of this kind happen
// while I was running?" without depending on timestamp resolution.
func (s *Store) LatestEventID() (int64, error) {
	var id sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(id) FROM events`).Scan(&id)
	return id.Int64, err
}

// CountEventsAfter counts events with an id greater than afterID whose type is
// one of types.
func (s *Store) CountEventsAfter(afterID int64, types ...string) (int, error) {
	if len(types) == 0 {
		return 0, nil
	}
	q := `SELECT COUNT(*) FROM events WHERE id > ? AND type IN (` + placeholders(len(types)) + `)`
	args := append([]any{afterID}, stringArgs(types)...)
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// CountSessionEventsAfter is CountEventsAfter restricted to one session's
// events, so a caller measuring its own session is not charged with what
// another session on the shared store did meanwhile.
func (s *Store) CountSessionEventsAfter(sessionID, afterID int64, types ...string) (int, error) {
	if len(types) == 0 {
		return 0, nil
	}
	q := `SELECT COUNT(*) FROM events WHERE session_id = ? AND id > ? AND type IN (` + placeholders(len(types)) + `)`
	args := append([]any{sessionID, afterID}, stringArgs(types)...)
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

func stringArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}
