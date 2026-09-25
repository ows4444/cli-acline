package store

import (
	"acline/internal/clip"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type MemoryEntry struct {
	ID         int64
	Area       sql.NullString
	Kind       string
	Body       string
	Status     string
	Stale      bool
	ProjectID  sql.NullInt64
	ActorType  sql.NullString
	ActorID    sql.NullString
	Model      sql.NullString
	ReviewedAt sql.NullString
	SourceKind sql.NullString
	SourceID   sql.NullInt64
	CreatedAt  string
}

// MemoryOpts is variadic on AddMemory so existing 3-arg call sites keep
// compiling unchanged; only callers that need project scoping pass one.
type MemoryOpts struct {
	ProjectID  *int64
	SourceKind string // "note", "check:<kind>", "rejection"; empty for a direct write
	SourceID   int64  // note id or task id, per SourceKind
	// ForcePending holds the entry for review even when the actor is human.
	// Auto-drafted entries set it: a person recording a failing check has not
	// vouched for the lesson drafted from it.
	ForcePending bool
}

var ValidMemoryKinds = map[string]bool{
	"constraint": true, "lesson": true, "pitfall": true,
	"operational": true, "failure_pattern": true,
}

var ValidMemoryStatuses = map[string]bool{
	"pending": true, "approved": true, "rejected": true,
}

// AddMemory records a durable lesson. Agent-written entries land in 'pending'
// and must be reviewed; human-written entries are approved on arrival.
func (s *Store) AddMemory(area, kind, body string, opts ...MemoryOpts) (int64, error) {
	body = scrubText(body)
	if kind == "" {
		kind = "lesson"
	}
	if !ValidMemoryKinds[kind] {
		return 0, fmt.Errorf("invalid memory kind %q", kind)
	}
	var opt MemoryOpts
	if len(opts) > 0 {
		opt = opts[0]
	}
	status := "pending"
	var reviewedAt any
	now := time.Now().UTC().Format(time.RFC3339)
	if s.Actor.Type == "human" && !opt.ForcePending {
		status = "approved"
		reviewedAt = now
	}
	res, err := s.DB.Exec(
		`INSERT INTO memory (area, kind, body, status, project_id, actor_type, actor_id, model, reviewed_at, source_kind, source_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullStr(area), kind, body, status, nullInt(opt.ProjectID), s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), reviewedAt,
		nullStr(opt.SourceKind), sourceIDArg(opt), now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.indexForSearch("memory", id, nullInt(opt.ProjectID), body); err != nil {
		return 0, err
	}
	s.indexForSemanticSearch("memory", id, nullInt(opt.ProjectID), body)
	return id, nil
}

func (s *Store) setMemoryStatus(id int64, status string) error {
	if !ValidMemoryStatuses[status] {
		return fmt.Errorf("invalid memory status %q", status)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	return s.changeWithEvent("memory entry", id, "memory_reviewed", fmt.Sprintf("memory #%d %s", id, status),
		`UPDATE memory SET status = ?, reviewed_at = ? WHERE id = ?`, status, now, id)
}

// DefaultMemoryDecayDays is how long an approved memory entry can go without
// being reconfirmed (`acline memory touch`) before it surfaces as a decay
// candidate. The CLI, the MCP server and the dashboard all use this one value.
const DefaultMemoryDecayDays = 90

// ErrAgentCannotReview is returned by ReviewMemory for an agent actor: entries
// written by an agent are held for review precisely so that someone other than
// the agent decides whether they become trusted context.
var ErrAgentCannotReview = errors.New("an agent cannot review memory entries: a human must approve or reject them")

// ReviewMemory approves or rejects a pending memory entry and records the
// decision in the audit trail. It is the single path behind `acline memory
// approve|reject` and the MCP acline_memory_approve|reject tools.
func (s *Store) ReviewMemory(id int64, approve bool, token string) error {
	// Approving is privileged: with an approval token enabled it must be
	// presented (and then also lets a server whose actor is an agent -- e.g. one
	// spawned by an editor -- act on a human's behalf). Rejecting is the safe
	// direction and needs no token, only a non-agent actor.
	viaToken := false
	if approve {
		var err error
		if viaToken, err = s.authorize(token); err != nil {
			return err
		}
	}
	if s.Actor.Type == "agent" && !viaToken {
		return ErrAgentCannotReview
	}
	status := "rejected"
	if approve {
		status = "approved"
	}
	return s.setMemoryStatus(id, status)
}

func (s *Store) SetMemoryStale(id int64, stale bool) error {
	res, err := s.DB.Exec(`UPDATE memory SET stale = ? WHERE id = ?`, stale, id)
	if err != nil {
		return err
	}
	return mustExist(res, "memory entry", id)
}

// TouchMemory resets an entry's decay clock (see DecayCandidates) by
// bumping reviewed_at to now, without otherwise changing it — the way a
// human reconfirms "still true" for an old lesson/constraint they just
// re-read, short of re-running the full approve flow.
func (s *Store) TouchMemory(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(`UPDATE memory SET reviewed_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return err
	}
	return mustExist(res, "memory entry", id)
}

// decayWhereClause is shared by DecayCandidates and CountDecayCandidates:
// approved, non-stale entries not reconfirmed (reviewed_at, falling back to
// created_at for an entry never explicitly reviewed) in at least `days`
// days. julianday() parses the RFC3339 timestamps this codebase stores
// throughout (format 5/6 in SQLite's own date-function docs, including the
// trailing "Z"), so no separate date parsing is needed here.
const decayWhereClause = `status = 'approved' AND stale = 0
	AND julianday('now') - julianday(COALESCE(reviewed_at, created_at)) >= ?`

// DecayCandidates lists approved memory entries that haven't been
// reconfirmed in at least `days` days — never auto-deleted or auto-marked
// stale, just surfaced for a human (or `acline reflect`) to decide: `acline
// memory touch <id>` to reconfirm it's still true, `acline memory forget
// <id>` if it no longer is. See `acline memory decay`.
func (s *Store) DecayCandidates(days int, projectID *int64) ([]MemoryEntry, error) {
	q := `SELECT ` + memoryColumns + ` FROM memory WHERE ` + decayWhereClause
	args := []any{days}
	if projectID != nil {
		q += ` AND project_id = ?`
		args = append(args, *projectID)
	}
	q += ` ORDER BY COALESCE(reviewed_at, created_at)`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemory(rows)
}

// CountDecayCandidates is DecayCandidates' count-only form, for the
// dashboard's end-of-session nudge (see CountPendingMemory).
func (s *Store) CountDecayCandidates(days int) (int, error) {
	return s.CountDecayCandidatesIn(days, nil)
}

// CountDecayCandidatesIn is CountDecayCandidates scoped to one project (nil = all).
func (s *Store) CountDecayCandidatesIn(days int, projectID *int64) (int, error) {
	q, args := `SELECT COUNT(*) FROM memory WHERE `+decayWhereClause, []any{days}
	q, args = scopeToProject(q, args, projectID)
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// scopeToProject appends `AND project_id = ?` when a project is given. q must
// already have a WHERE clause.
func scopeToProject(q string, args []any, projectID *int64) (string, []any) {
	if projectID == nil {
		return q, args
	}
	return q + ` AND project_id = ?`, append(args, *projectID)
}

type MemoryFilter struct {
	Status       string
	IncludeStale bool
	ProjectID    *int64
}

func (s *Store) ListMemory(f MemoryFilter) ([]MemoryEntry, error) {
	q := `SELECT ` + memoryColumns + ` FROM memory`
	var where []string
	var args []any
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	}
	if !f.IncludeStale {
		where = append(where, `stale = 0`)
	}
	if f.ProjectID != nil {
		where = append(where, `project_id = ?`)
		args = append(args, *f.ProjectID)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemory(rows)
}

// CountDraftedMemory counts pending entries that were auto-drafted from a
// failed check or a rejection (see draftFailurePattern), a subset of
// CountPendingMemory.
func (s *Store) CountDraftedMemory() (int, error) {
	return s.CountDraftedMemoryIn(nil)
}

// CountDraftedMemoryIn is CountDraftedMemory scoped to one project (nil = all).
func (s *Store) CountDraftedMemoryIn(projectID *int64) (int, error) {
	q, args := scopeToProject(`SELECT COUNT(*) FROM memory WHERE status = 'pending' AND stale = 0
		AND (source_kind LIKE 'check:%' OR source_kind = 'rejection')`, nil, projectID)
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

func (s *Store) CountPendingMemory() (int, error) {
	return s.CountPendingMemoryIn(nil)
}

// CountPendingMemoryIn is CountPendingMemory scoped to one project (nil = all).
func (s *Store) CountPendingMemoryIn(projectID *int64) (int, error) {
	q, args := scopeToProject(`SELECT COUNT(*) FROM memory WHERE status = 'pending' AND stale = 0`, nil, projectID)
	var n int
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

const memoryColumns = `id, area, kind, body, status, stale, project_id, actor_type, actor_id, model, reviewed_at, source_kind, source_id, created_at`

func scanMemory(rows *sql.Rows) ([]MemoryEntry, error) {
	var out []MemoryEntry
	for rows.Next() {
		var m MemoryEntry
		var stale int
		if err := rows.Scan(&m.ID, &m.Area, &m.Kind, &m.Body, &m.Status, &stale, &m.ProjectID,
			&m.ActorType, &m.ActorID, &m.Model, &m.ReviewedAt, &m.SourceKind, &m.SourceID, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Stale = stale != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func sourceIDArg(o MemoryOpts) any {
	if o.SourceKind == "" {
		return nil
	}
	return o.SourceID
}

const maxDraftDetail = 300

// draftFailurePattern records a pending failure_pattern memory for a failed
// check ("check:<kind>") or a rejection ("rejection") on a task. It is
// best-effort (the check or approval it follows is already recorded) and
// capped at one live draft per task and source, so a retry loop cannot flood
// the review queue. Drafts are always pending: a human must approve one
// before it counts as trusted context.
func (s *Store) draftFailurePattern(taskID int64, sourceKind, detail string) {
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM memory WHERE kind = 'failure_pattern' AND source_kind = ? AND source_id = ? AND status != 'rejected'`,
		sourceKind, taskID).Scan(&existing); err != nil || existing > 0 {
		return
	}
	task, err := s.GetTask(taskID)
	if err != nil {
		return
	}
	what := "Rejected at review"
	if k, ok := strings.CutPrefix(sourceKind, "check:"); ok {
		what = "Failed " + k + " check"
	}
	body := fmt.Sprintf("%s on task #%d (%s)", what, taskID, task.Title)
	if detail = strings.TrimSpace(detail); detail != "" {
		if len(detail) > maxDraftDetail {
			detail = clip.Bytes(detail, maxDraftDetail) + "..."
		}
		body += ": " + detail
	}
	var projectID *int64
	if task.ProjectID.Valid {
		projectID = &task.ProjectID.Int64
	}
	area := ""
	if task.Area.Valid {
		area = task.Area.String
	}
	id, err := s.AddMemory(area, "failure_pattern", body, MemoryOpts{
		ProjectID: projectID, SourceKind: sourceKind, SourceID: taskID, ForcePending: true,
	})
	if err != nil {
		return
	}
	s.LogEventGlobal("memory_drafted", fmt.Sprintf("memory #%d drafted from %s on task #%d (pending review)", id, sourceKind, taskID))
}

// RecallForTask returns the approved pitfall and failure_pattern memory
// relevant to a task: same area (or no area) within the task's project, or
// with no project. Pending, rejected and stale entries are never returned.
func (s *Store) RecallForTask(taskID int64) ([]MemoryEntry, error) {
	task, err := s.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + memoryColumns + ` FROM memory
		WHERE status = 'approved' AND stale = 0 AND kind IN ('pitfall', 'failure_pattern')
		AND (project_id IS NULL OR project_id = ?)
		AND (area IS NULL OR area = ?) ORDER BY id`
	rows, err := s.DB.Query(q, nullInt64Arg(task.ProjectID), nullStrArg(task.Area))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanMemory(rows)
	if err != nil {
		return nil, err
	}
	// Lessons in other areas can still be about the same thing: add the ones
	// whose meaning is close to the task, when embeddings are configured.
	seen := make(map[int64]bool, len(out))
	for _, m := range out {
		seen[m.ID] = true
	}
	var projectID *int64
	if task.ProjectID.Valid {
		projectID = &task.ProjectID.Int64
	}
	return append(out, s.semanticRecall(task.Title+"\n"+task.Description, projectID, seen)...), nil
}

func nullInt64Arg(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}

func nullStrArg(n sql.NullString) any {
	if n.Valid {
		return n.String
	}
	return nil
}
