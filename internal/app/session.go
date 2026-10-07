// This file starts and ends work sessions for every adapter: `acline session
// start|end` and acline_session_start|end each resolved the project and role,
// called the store, recalled lessons and counted the memory review queue
// themselves. The CLI resolved the project from the current directory before
// the task's, so a session for a task could be scoped to another project.
package app

import (
	"acline/internal/store"
)

// StartSessionRequest opens a session. Its project is ProjectArg, else the
// task's, else (with AllowCwdFallback) the current one; RoleArg is looked up in
// that project.
type StartSessionRequest struct {
	TaskID           *int64
	ProjectArg       string
	RoleArg          string
	AllowCwdFallback bool
	Policy           store.Policy
}

// StartSessionResult is the new session, the project it is scoped to (nil:
// unscoped) and the lessons recalled for its task.
type StartSessionResult struct {
	ID        int64
	ProjectID *int64
	Lessons   []store.MemoryEntry
}

// StartSession begins a session; with a task, the task moves to in_progress
// (store.BeginSession). store.ErrSessionActive comes back unwrapped. Recalling
// lessons is best-effort: a recall error never fails a session that began.
func StartSession(st *store.Store, req StartSessionRequest) (StartSessionResult, error) {
	var projectID *int64
	if req.TaskID != nil {
		t, err := st.GetTask(*req.TaskID)
		if err != nil {
			return StartSessionResult{}, err
		}
		if t.ProjectID.Valid {
			projectID = &t.ProjectID.Int64
		}
	}
	if req.ProjectArg != "" || projectID == nil {
		var err error
		if projectID, err = ResolveProject(st, req.ProjectArg, req.AllowCwdFallback); err != nil {
			return StartSessionResult{}, err
		}
	}
	roleID, err := st.ResolveRole(req.RoleArg, projectID)
	if err != nil {
		return StartSessionResult{}, err
	}
	id, err := st.BeginSession(store.SessionStart{TaskID: req.TaskID, ProjectID: projectID, RoleID: roleID, Policy: req.Policy})
	if err != nil {
		return StartSessionResult{}, err
	}
	res := StartSessionResult{ID: id, ProjectID: projectID}
	if req.TaskID != nil {
		res.Lessons, _ = st.RecallForTask(*req.TaskID)
	}
	return res, nil
}

// EndSessionResult is the ended session and how many memory entries wait for
// a person's review (the end-of-session ritual).
type EndSessionResult struct {
	Session       *store.Session
	PendingMemory int
}

// EndSession ends the current session (store.EndSessionWithToken: a
// restrictive policy or read-only role needs Token). store.ErrNoActiveSession
// and store.ErrApprovalTokenRequired come back unwrapped.
func EndSession(st *store.Store, summary string, cost store.SessionCost, token string) (EndSessionResult, error) {
	sess, err := st.EndSessionWithToken(summary, cost, token)
	if err != nil {
		return EndSessionResult{}, err
	}
	pending, err := st.CountPendingMemory()
	if err != nil {
		return EndSessionResult{}, err
	}
	return EndSessionResult{Session: sess, PendingMemory: pending}, nil
}
