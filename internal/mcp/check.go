package mcp

import (
	"context"
	"fmt"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/checkrun"
	"acline/internal/store"
	"acline/internal/worktree"
)

type checkOut struct {
	ID        int64   `json:"id"`
	TaskID    int64   `json:"task_id"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	Detail    *string `json:"detail,omitempty"`
	CreatedAt string  `json:"created_at"`
}

func toCheckOut(c store.Check) checkOut {
	return checkOut{ID: c.ID, TaskID: c.TaskID, Kind: c.Kind, Status: c.Status, Detail: nullStrPtr(c.Detail), CreatedAt: c.CreatedAt}
}

type checkRecordArgs struct {
	TaskID  int64  `json:"task_id" jsonschema:"the task this verification result applies to"`
	Kind    string `json:"kind" jsonschema:"test|sast|sca|lint|human_review|eval"`
	Status  string `json:"status" jsonschema:"pass|fail|skipped"`
	Detail  string `json:"detail,omitempty" jsonschema:"tool output, coverage, finding count, etc."`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
	Project string `json:"project,omitempty" jsonschema:"project name to resolve the role in"`
	Token   string `json:"token,omitempty" jsonschema:"human approval token; needed to record a passing human_review when the store has one enabled or the server's actor is an agent. Never read from the server's environment."`
}

type checkRecordOut struct {
	ID int64 `json:"id"`
}

type checkListArgs struct {
	TaskID int64 `json:"task_id" jsonschema:"the task id"`
	Limit  int   `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset int   `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type checkListOut struct {
	Checks    []checkOut `json:"checks"`
	Truncated bool       `json:"truncated,omitempty"`
}

func registerCheckTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_check_record",
		Description: "Record a verification result (test/sast/sca/lint/human_review/eval) against a task. Checks are append-only -- the newest result for a kind is what the completion gate looks at.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args checkRecordArgs) (*sdkmcp.CallToolResult, checkRecordOut, error) {
		if _, err := st.GetTask(args.TaskID); err != nil {
			return nil, checkRecordOut{}, err
		}
		roleID, err := resolveRole(st, args.Role, args.Project)
		if err != nil {
			return nil, checkRecordOut{}, err
		}
		cid, err := st.AddCheckWithMeta(args.TaskID, roleID, args.Kind, args.Status, args.Detail, args.Token,
			store.CheckMeta{Source: store.CheckSourceManual, TreeHash: treeForTask(st, args.TaskID)})
		if err != nil {
			return nil, checkRecordOut{}, err
		}
		st.LogTaskEvent(args.TaskID, "check", fmt.Sprintf("%s: %s", args.Kind, args.Status))
		return textResult(fmt.Sprintf("check #%d recorded: %s %s", cid, args.Kind, args.Status)), checkRecordOut{ID: cid}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_check_run",
		Description: "Run the default tool for a check kind (test|sast|sca|lint) in the task's project directory (the server's working directory for a task with no registered project) and record its real result: " +
			"pass only on exit 0, fail on a non-zero exit or timeout, skipped when the tool is not installed or the project has no default runner. " +
			"There is deliberately no command argument: an agent cannot choose what runs (a project's runner is set by a human via `acline check runner set`).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args checkRunArgs) (*sdkmcp.CallToolResult, checkRunOut, error) {
		task, err := st.GetTask(args.TaskID)
		if err != nil {
			return nil, checkRunOut{}, err
		}
		roleID, err := resolveRole(st, args.Role, args.Project)
		if err != nil {
			return nil, checkRunOut{}, err
		}
		// The task's project directory, not the server's cwd: the tree sealed with
		// the result below is fingerprinted from the same place.
		dir, err := dirForTask(st, args.TaskID)
		if err != nil {
			return nil, checkRunOut{}, err
		}
		// The command is never the caller's: it is the project's human-configured
		// runner if there is one, else the built-in default.
		command, err := st.CheckRunnerCommand(nullIntPtr(task.ProjectID), args.Kind)
		if err != nil {
			return nil, checkRunOut{}, err
		}
		stopProgress := reportProgress(ctx, req, "running "+args.Kind)
		res, err := checkrun.Run(ctx, args.Kind, dir, command, 0)
		stopProgress()
		if err != nil {
			return nil, checkRunOut{}, err
		}
		tree := worktree.Hash(dir)
		detail := res.Detail
		if tree == "" {
			detail += store.UnboundTreeNote
		}
		cid, err := st.AddCheckWithMeta(args.TaskID, roleID, args.Kind, res.Status, detail, "",
			store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: tree})
		if err != nil {
			return nil, checkRunOut{}, err
		}
		st.LogTaskEvent(args.TaskID, "check", fmt.Sprintf("%s: %s", args.Kind, res.Status))
		return textResult(fmt.Sprintf("check #%d recorded: %s %s", cid, args.Kind, res.Status)),
			checkRunOut{ID: cid, Status: res.Status, Detail: res.Detail}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_check_list",
		Description: "List verification results recorded against a task.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args checkListArgs) (*sdkmcp.CallToolResult, checkListOut, error) {
		checks, err := st.ListChecks(args.TaskID)
		if err != nil {
			return nil, checkListOut{}, err
		}
		page, truncated := paginate(checks, args.Limit, args.Offset)
		out := checkListOut{Checks: make([]checkOut, len(page)), Truncated: truncated}
		for i, c := range page {
			out.Checks[i] = toCheckOut(c)
		}
		return textResult(fmt.Sprintf("%d check(s) for task #%d", len(page), args.TaskID)), out, nil
	})
}

type checkRunArgs struct {
	TaskID  int64  `json:"task_id" jsonschema:"the task id"`
	Kind    string `json:"kind" jsonschema:"test|sast|sca|lint"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE or the active session's role)"`
	Project string `json:"project,omitempty" jsonschema:"project name for role resolution"`
}

type checkRunOut struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// progressEvery is how often a long tool call reports that it is still working.
var progressEvery = 5 * time.Second

// reportProgress sends a progress notification every progressEvery while a
// long call runs, if the client asked for progress (a progress token), so it
// can show that the call is alive and keep its request timeout from firing. The
// returned func stops it.
func reportProgress(ctx context.Context, req *sdkmcp.CallToolRequest, message string) func() {
	token := req.Params.GetProgressToken()
	if token == nil || req.Session == nil {
		return func() {}
	}
	done := make(chan struct{})
	start := time.Now()
	go func() {
		t := time.NewTicker(progressEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				elapsed := time.Since(start).Round(time.Second)
				_ = req.Session.NotifyProgress(ctx, &sdkmcp.ProgressNotificationParams{
					ProgressToken: token, Progress: elapsed.Seconds(), Message: fmt.Sprintf("%s (%s)", message, elapsed),
				})
			}
		}
	}()
	return func() { close(done) }
}
