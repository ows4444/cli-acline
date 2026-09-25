package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"acline/internal/scaffold"
)

type Session struct {
	ID        int64
	TaskID    sql.NullInt64
	ProjectID sql.NullInt64
	ActorType sql.NullString
	ActorID   sql.NullString
	Model     sql.NullString
	RoleID    sql.NullInt64
	Policy    sql.NullString
	TokensIn  int64
	TokensOut int64
	CostUSD   float64
	StartedAt string
	EndedAt   sql.NullString
	Summary   sql.NullString
}

// SessionCost is what a harness reports back when closing a session.
type SessionCost struct {
	TokensIn  int64
	TokensOut int64
	CostUSD   float64
}

var ErrNoActiveSession = errors.New("no active session")

// StartSession opens a session attributed to the resolved actor. Policy records
// what the agent was permitted to touch, for later incident reconstruction.
// projectID links the session to a project directly — the session <-> task
// link alone wasn't enough for dashboard/status to answer "what's active on
// project X", since a session need not have a task at all. roleID (nil if
// unset) becomes this session's ambient role for the rest of its lifetime —
// see Store.ResolveRole, which falls back to it when no --role/$ACLINE_ROLE
// is given on a later command.
func (s *Store) StartSession(taskID, projectID, roleID *int64, policy string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO sessions (task_id, project_id, actor_type, actor_id, model, role_id, policy, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(taskID), nullInt(projectID), s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), nullInt(roleID), nullStr(policy), now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const sessionColumns = `id, task_id, project_id, actor_type, actor_id, model, role_id, policy,
	tokens_in, tokens_out, cost_usd, started_at, ended_at, summary`

func scanSession(row interface{ Scan(...any) error }) (*Session, error) {
	sess := &Session{}
	if err := row.Scan(&sess.ID, &sess.TaskID, &sess.ProjectID, &sess.ActorType, &sess.ActorID, &sess.Model, &sess.RoleID,
		&sess.Policy, &sess.TokensIn, &sess.TokensOut, &sess.CostUSD,
		&sess.StartedAt, &sess.EndedAt, &sess.Summary); err != nil {
		return nil, err
	}
	return sess, nil
}

// CurrentSession is the active session that applies here: the one for the
// project the working directory resolves to, else an unscoped one (started
// with no project). A session of another project never applies, so its policy
// and role cannot judge this project's tool calls. Where no project resolves
// (a store that never registered one), it is the newest active session.
func (s *Store) CurrentSession() (*Session, error) {
	q := `SELECT ` + sessionColumns + ` FROM sessions WHERE ended_at IS NULL`
	var args []any
	if p, err := s.ResolveCurrentProject(); err == nil {
		// Prefer the project's own session over an unscoped one.
		q += ` AND (project_id = ? OR project_id IS NULL) ORDER BY project_id IS NULL, id DESC LIMIT 1`
		args = append(args, p.ID)
	} else {
		q += ` ORDER BY id DESC LIMIT 1`
	}
	sess, err := scanSession(s.DB.QueryRow(q, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoActiveSession
	}
	return sess, err
}

// StaleSessions lists active sessions with no activity (their start or their
// newest event) for longer than idle -- usually a crashed agent. They are not
// ended automatically: ending one lifts its policy and role, which is a
// person's call (see EndSessionWithToken). `acline doctor` reports them.
func (s *Store) StaleSessions(idle time.Duration) ([]Session, error) {
	cutoff := time.Now().Add(-idle).UTC().Format(time.RFC3339)
	rows, err := s.DB.Query(`SELECT `+sessionColumns+` FROM sessions s
		WHERE ended_at IS NULL
		AND MAX(started_at, COALESCE((SELECT MAX(created_at) FROM events e WHERE e.session_id = s.id), '')) < ?
		ORDER BY id`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

func (s *Store) EndSession(summary string, cost SessionCost) (*Session, error) {
	return s.EndSessionWithToken(summary, cost, "")
}

// isRestrictive reports whether a policy actually confines anything (a bare
// label, or an empty policy, does not).
func (p Policy) isRestrictive() bool {
	return len(p.AllowTools) > 0 || len(p.DenyTools) > 0 || len(p.AllowPaths) > 0 || len(p.DenyPaths) > 0
}

// EndSessionWithToken ends the active session. Ending it discards its policy,
// so when the session carries a restrictive one and an approval token is
// enabled, that token is required -- otherwise whoever a policy confines could
// simply end the session to lift it. Sessions without a restrictive policy, and
// stores without a token, behave as before.
func (s *Store) EndSessionWithToken(summary string, cost SessionCost, token string) (*Session, error) {
	summary = scrubText(summary)
	cur, err := s.CurrentSession()
	if err != nil {
		return nil, err
	}
	if ParsePolicy(cur.Policy.String).isRestrictive() {
		if _, err := s.authorize(token); err != nil {
			return nil, err
		}
	}
	if s.sessionRoleIsReadOnly(cur) {
		if err := s.requirePerson(token, ErrAgentCannotLeaveReadOnlyRole); err != nil {
			return nil, err
		}
	}
	return s.finishSession(cur, summary, cost)
}

// ErrAgentCannotLeaveReadOnlyRole is returned when an agent ends a session
// running as a read-only role (security, qa, architect, designer). The guard
// denies that session's file writes; if the agent could end it, it could start
// again as a role that may write.
var ErrAgentCannotLeaveReadOnlyRole = errors.New("an agent cannot end a session running as a read-only role: a person ends it (or the process that launched it)")

// sessionRoleIsReadOnly reports whether sess runs as a built-in role whose
// contract grants no file writes (see scaffold.RoleIsReadOnly).
func (s *Store) sessionRoleIsReadOnly(sess *Session) bool {
	if !sess.RoleID.Valid {
		return false
	}
	r, err := s.GetRole(sess.RoleID.Int64)
	return err == nil && scaffold.RoleIsReadOnly(r.Name)
}

// EndLaunchedSession ends session id for the process that began it (the
// orchestrator, which runs as an agent and launches read-only steps). It skips
// the read-only-role rule, which exists to stop the agent *inside* the session
// from lifting its own role; no CLI command or MCP tool calls it. It refuses
// when id is not the active session.
func (s *Store) EndLaunchedSession(id int64, summary string, cost SessionCost) (*Session, error) {
	// By id, not CurrentSession: the launcher's working directory need not
	// resolve to the session's project.
	cur, err := scanSession(s.DB.QueryRow(`SELECT `+sessionColumns+` FROM sessions WHERE id = ? AND ended_at IS NULL`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("session #%d is not active", id)
	}
	if err != nil {
		return nil, err
	}
	return s.finishSession(cur, scrubText(summary), cost)
}

func (s *Store) finishSession(cur *Session, summary string, cost SessionCost) (*Session, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.Exec(
		`UPDATE sessions SET ended_at = ?, summary = ?, tokens_in = ?, tokens_out = ?, cost_usd = ? WHERE id = ? AND ended_at IS NULL`,
		now, summary, cost.TokensIn, cost.TokensOut, cost.CostUSD, cur.ID,
	)
	if err != nil {
		return nil, err
	}
	cur.EndedAt = sql.NullString{String: now, Valid: true}
	cur.Summary = sql.NullString{String: summary, Valid: summary != ""}
	cur.TokensIn, cur.TokensOut, cur.CostUSD = cost.TokensIn, cost.TokensOut, cost.CostUSD
	return cur, nil
}

func (s *Store) ListSessions(projectID *int64, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT ` + sessionColumns + ` FROM sessions`
	args := []any{}
	if projectID != nil {
		q += ` WHERE project_id = ?`
		args = append(args, *projectID)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

// ErrSessionActive is returned by BeginSession when one is already running.
var ErrSessionActive = errors.New("a session is already active; end it first")

// SessionStart describes a session to begin.
type SessionStart struct {
	TaskID    *int64 // optional: also moves the task to in_progress
	ProjectID *int64 // optional: defaults to the task's project
	RoleID    *int64
	Policy    Policy
}

// BeginSession starts a session. Everything it implies -- refusing a second
// active session, moving the task to in_progress, its status_change event and
// the session row -- happens in one transaction, so two racing starts cannot
// both succeed and a failure cannot leave a task in_progress with no session.
func (s *Store) BeginSession(r SessionStart) (int64, error) {
	projectID := r.ProjectID
	if r.TaskID != nil {
		task, err := s.GetTask(*r.TaskID)
		if err != nil {
			return 0, err
		}
		if projectID == nil && task.ProjectID.Valid {
			projectID = &task.ProjectID.Int64
		}
	}
	policyJSON, err := r.Policy.JSON()
	if err != nil {
		return 0, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// One active session per project. An unscoped session (no project) applies
	// everywhere, so it conflicts with any other, and every session conflicts
	// with it.
	var active int
	conflict := `SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL`
	var cargs []any
	if projectID != nil {
		conflict += ` AND (project_id = ? OR project_id IS NULL)`
		cargs = append(cargs, *projectID)
	}
	if err := tx.QueryRow(conflict, cargs...).Scan(&active); err != nil {
		return 0, err
	}
	if active > 0 {
		return 0, ErrSessionActive
	}
	if r.TaskID != nil {
		if err := updateTaskStatus(tx, *r.TaskID, "in_progress"); err != nil {
			return 0, err
		}
		if _, err := s.logEventTx(tx, r.TaskID, nil, nil, "status_change", "status -> in_progress"); err != nil {
			return 0, err
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := tx.Exec(
		`INSERT INTO sessions (task_id, project_id, actor_type, actor_id, model, role_id, policy, started_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		nullInt(r.TaskID), nullInt(projectID), s.Actor.Type, s.Actor.ID, nullStr(s.Actor.Model), nullInt(r.RoleID), nullStr(policyJSON), now,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}
