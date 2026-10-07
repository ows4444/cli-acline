package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

type taskOut struct {
	ID          int64   `json:"id"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	Status      string  `json:"status"`
	Priority    string  `json:"priority"`
	Area        *string `json:"area,omitempty"`
	Risk        string  `json:"risk"`
	Autonomy    string  `json:"autonomy"`
	SpecID      *int64  `json:"spec_id,omitempty"`
	ProjectID   *int64  `json:"project_id,omitempty"`
	// BlockedReason is why the task is blocked, when it is and one was given.
	BlockedReason *string `json:"blocked_reason,omitempty"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
	// NeedsAttention is computed server-side (store.Task.NeedsAttention) so
	// every client shows the same set of tasks. Always sent by this binary,
	// but the ts:"optional" tag keeps the generated TS type optional since
	// it's absent from servers older than acline 0.2.
	NeedsAttention bool `json:"needs_attention" ts:"optional"`
}

func toTaskOut(t store.Task) taskOut {
	return taskOut{
		BlockedReason: nullStrPtr(t.BlockedReason),
		ID:            t.ID, Title: t.Title, Description: t.Description, Status: t.Status, Priority: t.Priority,
		Area: nullStrPtr(t.Area), Risk: t.Risk, Autonomy: t.Autonomy,
		SpecID: nullIntPtr(t.SpecID), ProjectID: nullIntPtr(t.ProjectID),
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, NeedsAttention: t.NeedsAttention(),
	}
}

type taskListArgs struct {
	Status  string `json:"status,omitempty" jsonschema:"backlog|todo|in_progress|blocked|review|done|cancelled"`
	Area    string `json:"area,omitempty" jsonschema:"filter by ownership area"`
	Risk    string `json:"risk,omitempty" jsonschema:"low|medium|high|critical"`
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	All     bool   `json:"all,omitempty" jsonschema:"include deferred/archived tasks (excluded by default)"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type taskListOut struct {
	Tasks     []taskOut `json:"tasks"`
	Truncated bool      `json:"truncated,omitempty"`
}

type taskAddArgs struct {
	Title       string `json:"title" jsonschema:"the task title"`
	Description string `json:"description,omitempty"`
	Priority    string `json:"priority,omitempty" jsonschema:"low|normal|high|urgent (default normal)"`
	Area        string `json:"area,omitempty" jsonschema:"ownership area, e.g. cli, store, hooks, skills, docs"`
	Risk        string `json:"risk,omitempty" jsonschema:"low|medium|high|critical (default: the project's, else low)"`
	Autonomy    string `json:"autonomy,omitempty" jsonschema:"hitl|hotl|auto (default: the project's, else hotl); auto needs a person (or the token)"`
	Type        string `json:"type,omitempty" jsonschema:"bug|refactor|test|architecture|security|performance|reliability|contract|database|messaging|ui_ux|accessibility|feature|debt|docs|devex"`
	SpecID      *int64 `json:"spec_id,omitempty" jsonschema:"the spec this task is derived from"`
	MilestoneID *int64 `json:"milestone_id,omitempty" jsonschema:"the milestone this task belongs to"`
	ParentID    *int64 `json:"parent_id,omitempty" jsonschema:"parent task id (a subtask; grouping only, it does not make the parent wait)"`
	Project     string `json:"project,omitempty" jsonschema:"project name to scope this task to"`
	Role        string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
	Token       string `json:"token,omitempty" jsonschema:"human approval token; needed for autonomy auto when the store has one enabled or the server's actor is an agent. Never read from the server's environment."`
}

type taskAddOut struct {
	ID int64 `json:"id"`
}

func registerTaskTools(s *sdkmcp.Server, st *store.Store) {
	addTaskListTool(s, st)
	addTaskAddTool(s, st)
}

func addTaskListTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_task_list",
		Description: "List work items (tasks), optionally filtered by status, area, risk, or project.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskListArgs) (*sdkmcp.CallToolResult, taskListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, taskListOut{}, err
		}
		tasks, err := st.ListTasks(store.TaskFilter{Status: args.Status, Area: args.Area, Risk: args.Risk, All: args.All, ProjectID: projectID})
		if err != nil {
			return nil, taskListOut{}, err
		}
		page, truncated := paginate(tasks, args.Limit, args.Offset)
		out := taskListOut{Tasks: make([]taskOut, len(page)), Truncated: truncated}
		for i, t := range page {
			out.Tasks[i] = toTaskOut(t)
		}
		return textResult(fmt.Sprintf("%d task(s)", len(page))), out, nil
	})
}

func addTaskAddTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_task_add",
		Description: "Add a work item. Does not evaluate or bypass the completion gate -- that only applies to `task done`, not to creating a task.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskAddArgs) (*sdkmcp.CallToolResult, taskAddOut, error) {
		id, err := app.AddTask(st, app.AddTaskRequest{
			Title: args.Title, Description: args.Description, Priority: args.Priority, Area: args.Area, Type: args.Type,
			Risk: args.Risk, Autonomy: args.Autonomy, SpecID: args.SpecID, MilestoneID: args.MilestoneID, ParentID: args.ParentID,
			Token: args.Token, RoleArg: args.Role, ProjectArg: args.Project,
		})
		if err != nil {
			return nil, taskAddOut{}, err
		}
		return textResult(fmt.Sprintf("task #%d added", id)), taskAddOut{ID: id}, nil
	})
}
