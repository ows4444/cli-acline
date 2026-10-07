// This file creates tasks for every adapter: `acline task add` and
// acline_task_add each resolved a project and role and called store.AddTask
// themselves, and only the MCP tool refused a blank title.
package app

import (
	"errors"
	"strings"

	"acline/internal/store"
)

// ErrTitleRequired is a task created with a blank title.
var ErrTitleRequired = errors.New("title is required")

var (
	// ErrNothingToUpdate is a task update that names no field.
	ErrNothingToUpdate = errors.New("nothing to update")
	// ErrReasonNeedsBlocked is a reason given without status "blocked".
	ErrReasonNeedsBlocked = errors.New("a reason only applies to status blocked")
)

// AddTaskRequest is a new task. Empty Priority, Risk and Autonomy take their
// defaults (Risk and Autonomy from the task's project first, see
// store.AddTask). Token is consulted only for autonomy "auto".
type AddTaskRequest struct {
	Title            string
	Description      string
	Priority         string
	Area             string
	Type             string
	Risk             string
	Autonomy         string
	SpecID           *int64
	MilestoneID      *int64
	ParentID         *int64
	Token            string
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
}

// AddTask creates a task in the request's project (resolved as ResolveProject
// does) under its role. Invalid values and missing spec, milestone, parent or
// project come back from the store; store.ErrApprovalTokenRequired comes back
// unwrapped so the CLI can prompt for the token.
func AddTask(st *store.Store, req AddTaskRequest) (int64, error) {
	if strings.TrimSpace(req.Title) == "" {
		return 0, ErrTitleRequired
	}
	projectID, err := ResolveProject(st, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	roleID, err := ResolveRole(st, req.RoleArg, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	return st.AddTask(req.Title, req.Description, req.Priority, store.TaskOpts{
		Area: req.Area, Type: req.Type, Risk: req.Risk, Autonomy: req.Autonomy,
		SpecID: req.SpecID, MilestoneID: req.MilestoneID, ParentID: req.ParentID,
		ProjectID: projectID, RoleID: roleID, Token: req.Token,
	})
}

// UpdateTask changes the fields u names, all or none (store.UpdateTask), and
// returns the ones that changed. store.ErrStatusDoneNeedsGate and
// store.ErrApprovalTokenRequired come back unwrapped for each adapter to word
// or prompt on.
func UpdateTask(st *store.Store, id int64, u store.TaskUpdate) ([]string, error) {
	if u.Status == "" && u.Priority == "" && u.Area == "" && u.Type == "" && u.Risk == "" &&
		u.Autonomy == "" && u.Milestone == nil && !u.ClearMilestone {
		return nil, ErrNothingToUpdate
	}
	if u.Reason != "" && u.Status != "blocked" {
		return nil, ErrReasonNeedsBlocked
	}
	return st.UpdateTask(id, u)
}

// CompleteTaskRequest marks a task done. Force overrides an unsatisfied gate
// and needs a person (or Token). Hash fingerprints a directory (worktree.Hash,
// or a test's stand-in).
type CompleteTaskRequest struct {
	TaskID int64
	Force  bool
	Token  string
	Hash   func(dir string) string
}

// CompleteTask evaluates the completion gate against the task's current tree
// (GateTree) and marks the task done when it passes or is forced. A
// *store.GateBlockedError and store.ErrApprovalTokenRequired come back
// unwrapped for each adapter to word or prompt on. A missing task is
// store.ErrNotFound (the gate's own queries would fail on it with a scan error).
func CompleteTask(st *store.Store, req CompleteTaskRequest) (store.CompleteResult, error) {
	if _, err := st.GetTask(req.TaskID); err != nil {
		return store.CompleteResult{}, err
	}
	return st.CompleteTaskForTree(req.TaskID, req.Force, req.Token, GateTree(st, req.TaskID, req.Hash))
}
