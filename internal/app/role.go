// This file hands a task to a role for every adapter: `acline task assign`
// and acline_task_assign each looked up the role, assigned it, named the
// previous role and asked for the advisory next one themselves. The CLI also
// looked the role up in the current directory's project before the task's,
// so a same-named role of another project could be assigned.
package app

import (
	"database/sql"
	"errors"
	"fmt"

	"acline/internal/store"
)

// ErrRoleOfAnotherProject is a role that belongs to a project other than the
// task's.
var ErrRoleOfAnotherProject = errors.New("the role belongs to another project than the task")

// AssignRoleRequest hands a task to a role, looked up by name in ProjectArg's
// scope, else the task's own project (global roles plus that project's).
type AssignRoleRequest struct {
	TaskID     int64
	Role       string
	ProjectArg string
}

// AssignRoleResult is the role now owning the task, the one before it
// ("unassigned" if none) and the advisory next stage, if any.
type AssignRoleResult struct {
	Role     *store.Role
	Previous string
	Next     *string
}

// AssignRole sets the task's owning role (store.AssignRole records the
// role_assigned event). A role of a project other than the task's is refused.
func AssignRole(st *store.Store, req AssignRoleRequest) (AssignRoleResult, error) {
	t, err := st.GetTask(req.TaskID)
	if err != nil {
		return AssignRoleResult{}, err
	}
	projectID, err := ResolveProject(st, req.ProjectArg, false)
	if err != nil {
		return AssignRoleResult{}, err
	}
	if projectID == nil && t.ProjectID.Valid {
		projectID = &t.ProjectID.Int64
	}
	role, err := st.GetRoleByName(projectID, req.Role)
	if err != nil {
		return AssignRoleResult{}, err
	}
	if role.ProjectID.Valid && t.ProjectID.Valid && role.ProjectID.Int64 != t.ProjectID.Int64 {
		return AssignRoleResult{}, fmt.Errorf("role %q, task #%d: %w", role.Name, t.ID, ErrRoleOfAnotherProject)
	}
	prev, err := st.AssignRole(req.TaskID, &role.ID)
	if err != nil {
		return AssignRoleResult{}, err
	}
	res := AssignRoleResult{Role: role, Previous: "unassigned"}
	if prev.Valid {
		if pr, err := st.GetRole(prev.Int64); err == nil {
			res.Previous = pr.Name
		}
	}
	if hint, err := st.NextRoleHint(projectID, sql.NullInt64{Int64: role.ID, Valid: true}); err == nil && hint != nil {
		res.Next = &hint.Name
	}
	return res, nil
}
