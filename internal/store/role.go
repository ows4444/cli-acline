package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
)

// Role is a named capacity a contributor (human or agent) acts in --
// developer, qa, designer, manager, scrummaster, or a project's own custom
// addition. See the `roles` table comment in store.go for the field
// semantics (project_id NULL = global/built-in, can_approve gates
// EvaluateGate, stage_order is advisory only).
type Role struct {
	ID          int64
	ProjectID   sql.NullInt64
	Name        string
	Kind        string
	CanApprove  bool
	StageOrder  sql.NullInt64
	Description sql.NullString
	CreatedAt   string
}

// ValidRoleKinds mirrors ValidRisks/ValidAutonomy's pattern: a fixed,
// validated enum rather than free text.
var ValidRoleKinds = map[string]bool{"human": true, "agent": true, "both": true}

// AddRole creates a project-scoped role. The 7 global (project_id NULL)
// roles are seeded once by migrations 4 and 5 and are not created through
// this function -- it always requires a project, so "who can create a
// global role" is never an ambiguous question.
func (s *Store) AddRole(projectID int64, name, kind string, canApprove bool, stageOrder *int64, description string) (int64, error) {
	return s.AddRoleWithToken(projectID, name, kind, canApprove, stageOrder, description, "")
}

// ErrAgentCannotCreateApprovingRole is returned when an agent tries to create a
// can_approve role. Such a role is what makes an approval count once a project
// opts into role enforcement, so an agent that could mint one could satisfy the
// gate it is subject to.
var ErrAgentCannotCreateApprovingRole = errors.New("an agent cannot create a role that can approve: a person must")

// ErrAgentCannotUseHumanRole is returned when an agent tries to act as a role
// whose kind is "human" (manager, scrummaster, or a project's own human role).
// The kind was stored and shown but never compared with who was acting.
var ErrAgentCannotUseHumanRole = errors.New("an agent cannot act as a human-only role")

// AddRoleWithToken is AddRole plus the approval token. A can_approve role is
// what makes an approval count once a project opts into role enforcement, so
// creating one is as privileged as approving: an agent needs the token, and
// with a token enabled everyone does (otherwise whoever is being gated could
// mint themselves an approving role). Non-approving roles never need it.
func (s *Store) AddRoleWithToken(projectID int64, name, kind string, canApprove bool, stageOrder *int64, description, token string) (int64, error) {
	if canApprove {
		if err := s.requirePerson(token, ErrAgentCannotCreateApprovingRole); err != nil {
			return 0, err
		}
	}
	if name == "" {
		return 0, fmt.Errorf("role name is required")
	}
	if kind == "" {
		kind = "both"
	}
	if !ValidRoleKinds[kind] {
		return 0, fmt.Errorf("invalid role kind %q (want: human|agent|both)", kind)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.Exec(
		`INSERT INTO roles (project_id, name, kind, can_approve, stage_order, description, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		projectID, name, kind, boolToInt(canApprove), nullInt(stageOrder), nullStr(description), now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListRoles returns every role visible to projectID: the 7 global roles
// plus, when projectID is non-nil, that project's own additions. Global
// roles first, then project-scoped, each ordered by stage_order (NULLs
// last) then name -- a natural "pipeline order" reading.
func (s *Store) ListRoles(projectID *int64) ([]Role, error) {
	rows, err := s.DB.Query(
		`SELECT id, project_id, name, kind, can_approve, stage_order, description, created_at
		 FROM roles
		 WHERE project_id IS NULL OR project_id = ?
		 ORDER BY project_id IS NOT NULL, stage_order IS NULL, stage_order, name`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []Role
	for rows.Next() {
		var r Role
		var canApprove int
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind, &canApprove, &r.StageOrder, &r.Description, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.CanApprove = canApprove != 0
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// GetRoleByName resolves name against projectID's own roles first, falling
// back to the global (project_id NULL) roles -- a project-scoped role can
// shadow a global one of the same name.
// GetRole looks up a role by id -- for display (e.g. `task show`, `check
// list`), where a row carries a role_id and the caller wants its name.
func (s *Store) GetRole(id int64) (*Role, error) {
	var r Role
	var canApprove int
	err := s.DB.QueryRow(
		`SELECT id, project_id, name, kind, can_approve, stage_order, description, created_at
		 FROM roles WHERE id = ?`,
		id,
	).Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind, &canApprove, &r.StageOrder, &r.Description, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("role #%d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	r.CanApprove = canApprove != 0
	return &r, nil
}

func (s *Store) GetRoleByName(projectID *int64, name string) (*Role, error) {
	var r Role
	var canApprove int
	err := s.DB.QueryRow(
		`SELECT id, project_id, name, kind, can_approve, stage_order, description, created_at
		 FROM roles
		 WHERE name = ? AND (project_id IS NULL OR project_id = ?)
		 ORDER BY project_id IS NULL
		 LIMIT 1`,
		name, projectID,
	).Scan(&r.ID, &r.ProjectID, &r.Name, &r.Kind, &canApprove, &r.StageOrder, &r.Description, &r.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("no role named %q (see `acline role list`)", name)
	}
	if err != nil {
		return nil, err
	}
	r.CanApprove = canApprove != 0
	return &r, nil
}

// NextRoleHint returns the role with the next-higher stage_order than
// currentRoleID within projectID's scope (global roles plus that project's
// own), or nil if currentRoleID is unset, has no stage_order, or is
// already the last stage. This is advisory only -- `acline task assign`
// never enforces it, it's just a suggestion surfaced in `task show`.
func (s *Store) NextRoleHint(projectID *int64, currentRoleID sql.NullInt64) (*Role, error) {
	if !currentRoleID.Valid {
		return nil, nil
	}
	cur, err := s.GetRole(currentRoleID.Int64)
	if err != nil || !cur.StageOrder.Valid {
		return nil, nil
	}
	roles, err := s.ListRoles(projectID)
	if err != nil {
		return nil, err
	}
	var next *Role
	for i := range roles {
		r := &roles[i]
		if !r.StageOrder.Valid || r.StageOrder.Int64 <= cur.StageOrder.Int64 {
			continue
		}
		if next == nil || r.StageOrder.Int64 < next.StageOrder.Int64 {
			next = r
		}
	}
	return next, nil
}

// ResolveRole resolves the active role id for this invocation, in order:
//  1. explicit (the --role flag's value, if the caller passed one)
//  2. $ACLINE_ROLE
//  3. the active session's stored role_id, if any (via CurrentSession,
//     the same source log.go/gate.go already use for session_id)
//
// A completely unset role (no flag, no env var, no session, or a session
// with no role) returns (nil, nil) -- not an error -- which is what keeps
// every command's INSERT identical to its pre-roles behavior. An explicit
// or env-var name that doesn't resolve to a real role IS an error, since
// that's very likely a typo the caller should see immediately rather than
// silently falling through to "no role".
func (s *Store) ResolveRole(explicit string, projectID *int64) (*int64, error) {
	name := explicit
	if name == "" {
		name = os.Getenv("ACLINE_ROLE")
	}
	if name != "" {
		r, err := s.GetRoleByName(projectID, name)
		if err != nil {
			return nil, err
		}
		if err := s.checkRoleKind(r); err != nil {
			return nil, err
		}
		return &r.ID, nil
	}
	if sess, err := s.CurrentSession(); err == nil && sess.RoleID.Valid {
		id := sess.RoleID.Int64
		if r, err := s.GetRole(id); err == nil {
			if err := s.checkRoleKind(r); err != nil {
				return nil, err
			}
		}
		return &id, nil
	}
	return nil, nil
}

// checkRoleKind refuses an agent a human-only role. A person may act as any role.
func (s *Store) checkRoleKind(r *Role) error {
	if r.Kind == "human" && s.Actor.Type == "agent" {
		return fmt.Errorf("%w: %q is for people (use one of the agent roles: developer, qa, architect, security, designer)", ErrAgentCannotUseHumanRole, r.Name)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
