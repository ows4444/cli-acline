// This file records verification results for every adapter: `acline check
// record`/`check run` and acline_check_record/acline_check_run each looked up
// the task, resolved a role, found the task's directory, fingerprinted its tree
// and called store.AddCheckWithMeta themselves.
package app

import (
	"context"
	"io"
	"time"

	"acline/internal/checkrun"
	"acline/internal/store"
)

// RecordCheckRequest is a hand-recorded check result. Hash fingerprints a
// directory (worktree.Hash, or a test's stand-in).
type RecordCheckRequest struct {
	TaskID           int64
	Kind             string
	Status           string
	Detail           string
	Token            string
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
	Hash             func(dir string) string
}

// RecordCheck records a hand-recorded result against an existing task, tied to
// the task's tree so the gate can tell when the code has changed since. Store
// errors (store.ErrAgentCannotRecordHumanReview, store.ErrApprovalTokenRequired)
// come back unwrapped for each adapter to word.
func RecordCheck(st *store.Store, req RecordCheckRequest) (int64, error) {
	if _, err := st.GetTask(req.TaskID); err != nil {
		return 0, err
	}
	roleID, err := ResolveRole(st, req.RoleArg, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, err
	}
	return st.AddCheckWithMeta(req.TaskID, roleID, req.Kind, req.Status, req.Detail, req.Token,
		store.CheckMeta{Source: store.CheckSourceManual, TreeHash: TaskTree(st, req.TaskID, req.Hash)})
}

// RunCheckRequest runs a kind's tool against a task. Command is a person's
// ad hoc choice (CLI --cmd) and needs Token from an agent; "" means the
// project's runner, else the built-in default. Timeout 0 is checkrun's default.
type RunCheckRequest struct {
	TaskID           int64
	Kind             string
	Command          string
	Token            string
	Timeout          time.Duration
	RoleArg          string
	ProjectArg       string
	AllowCwdFallback bool
	Hash             func(dir string) string
	// Output, when set, receives the tool's output as it runs.
	Output io.Writer
}

// RunCheck runs the tool in the task's project root (TaskDir: from a subfolder
// only that subfolder's tests would run) and records its result as runner
// evidence. An ad hoc command is authorized before it runs, so a refused agent
// never executes it.
func RunCheck(ctx context.Context, st *store.Store, req RunCheckRequest) (int64, checkrun.Result, error) {
	task, err := st.GetTask(req.TaskID)
	if err != nil {
		return 0, checkrun.Result{}, err
	}
	roleID, err := ResolveRole(st, req.RoleArg, req.ProjectArg, req.AllowCwdFallback)
	if err != nil {
		return 0, checkrun.Result{}, err
	}
	dir, err := TaskDir(st, req.TaskID)
	if err != nil {
		return 0, checkrun.Result{}, err
	}
	command, adHoc := req.Command, req.Command != ""
	if adHoc {
		if err := st.AuthorizeAdHocCheckCommand(req.Token); err != nil {
			return 0, checkrun.Result{}, err
		}
	} else {
		var projectID *int64
		if task.ProjectID.Valid {
			projectID = &task.ProjectID.Int64
		}
		if command, err = st.CheckRunnerCommand(projectID, req.Kind); err != nil {
			return 0, checkrun.Result{}, err
		}
	}
	res, err := checkrun.RunTo(ctx, req.Kind, dir, command, req.Timeout, req.Output)
	if err != nil {
		return 0, checkrun.Result{}, err
	}
	// Fingerprint after the run, so it matches what the gate sees later (the
	// tool may have written files that stay in the directory).
	tree := req.Hash(dir)
	detail := res.Detail
	if tree == "" {
		detail += store.UnboundTreeNote
	}
	cid, err := st.AddCheckWithMeta(req.TaskID, roleID, req.Kind, res.Status, detail, req.Token,
		store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: tree, AdHocCommand: adHoc})
	if err != nil {
		return 0, checkrun.Result{}, err
	}
	return cid, res, nil
}
