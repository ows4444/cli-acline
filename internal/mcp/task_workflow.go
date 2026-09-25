package mcp

import (
	"context"
	"errors"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// Workflow tools that close CLI gaps for MCP clients (and so for the VS Code
// extension): moving a task between statuses, and working with acceptance
// criteria -- which the completion gate reports on ("N criteria still
// unchecked") but that no MCP client could previously list or resolve.

type taskSetStatusArgs struct {
	ID     int64  `json:"id" jsonschema:"the task id"`
	Status string `json:"status" jsonschema:"backlog|todo|in_progress|blocked|review|cancelled (not done: use acline_task_done, which evaluates the gate)"`
	Reason string `json:"reason,omitempty" jsonschema:"why the task is blocked (only with status blocked); shown by acline_task_route"`
}

type taskSetStatusOut struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

type criterionOut struct {
	ID        int64   `json:"id"`
	TaskID    int64   `json:"task_id"`
	Text      string  `json:"text"`
	Pattern   *string `json:"pattern,omitempty"`
	Done      bool    `json:"done"`
	CreatedAt string  `json:"created_at"`
}

type criteriaListArgs struct {
	TaskID int64 `json:"task_id" jsonschema:"the task id"`
}

type criteriaListOut struct {
	Criteria []criterionOut `json:"criteria"`
}

type criterionAddArgs struct {
	TaskID int64  `json:"task_id" jsonschema:"the task id"`
	Text   string `json:"text" jsonschema:"the criterion, ideally EARS: 'When X, the system shall Y'"`
}

type criterionAddOut struct {
	ID      int64  `json:"id"`
	Pattern string `json:"pattern,omitempty"`
}

type criterionCheckArgs struct {
	ID   int64 `json:"id" jsonschema:"the criterion id"`
	Done *bool `json:"done,omitempty" jsonschema:"true (default) marks it done; false reopens it"`
}

type criterionCheckOut struct {
	ID   int64 `json:"id"`
	Done bool  `json:"done"`
}

type taskLinkArgs struct {
	TaskID        int64  `json:"task_id" jsonschema:"the task id"`
	Relation      string `json:"relation" jsonschema:"depends_on|blocks|related"`
	RelatedTaskID int64  `json:"related_task_id" jsonschema:"the other task id"`
}

type taskLinkOut struct {
	ID int64 `json:"id"`
}

type taskDeferArgs struct {
	ID      int64  `json:"id" jsonschema:"the task id"`
	Clear   bool   `json:"clear,omitempty" jsonschema:"clear the deferred flag instead of setting it"`
	Reason  string `json:"reason,omitempty" jsonschema:"required unless clear=true"`
	Trigger string `json:"trigger,omitempty" jsonschema:"what should bring this back up, e.g. 'after v2 ships'"`
}

type taskDeferOut struct {
	ID       int64 `json:"id"`
	Deferred bool  `json:"deferred"`
}

func registerTaskWorkflowTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_task_set_status",
		Description: "Move a task to a status other than done, recording a status_change event atomically. " +
			"Completing a task is acline_task_done, which evaluates the verification/approval gate; asking for 'done' here is refused.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskSetStatusArgs) (*sdkmcp.CallToolResult, taskSetStatusOut, error) {
		err := st.SetTaskStatusWithReason(args.ID, args.Status, args.Reason)
		if errors.Is(err, store.ErrStatusDoneNeedsGate) {
			return nil, taskSetStatusOut{}, fmt.Errorf("%w (acline_task_done)", err)
		}
		if err != nil {
			return nil, taskSetStatusOut{}, err
		}
		return textResult(fmt.Sprintf("task #%d -> %s", args.ID, args.Status)), taskSetStatusOut{ID: args.ID, Status: args.Status}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_criteria_list",
		Description: "List a task's acceptance criteria and whether each is done. Unchecked criteria are reported as warnings by the completion gate.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args criteriaListArgs) (*sdkmcp.CallToolResult, criteriaListOut, error) {
		if _, err := st.GetTask(args.TaskID); err != nil {
			return nil, criteriaListOut{}, err
		}
		list, err := st.ListCriteria(args.TaskID)
		if err != nil {
			return nil, criteriaListOut{}, err
		}
		out := criteriaListOut{Criteria: make([]criterionOut, len(list))}
		for i, c := range list {
			out.Criteria[i] = criterionOut{ID: c.ID, TaskID: c.TaskID, Text: c.Text, Pattern: nullStrPtr(c.Pattern), Done: c.Done, CreatedAt: c.CreatedAt}
		}
		return textResult(fmt.Sprintf("%d criteri(a) on task #%d", len(list), args.TaskID)), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_criteria_add",
		Description: "Add an acceptance criterion to a task (EARS phrasing 'When X, the system shall Y' is recognized and recorded).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args criterionAddArgs) (*sdkmcp.CallToolResult, criterionAddOut, error) {
		if args.Text == "" {
			return nil, criterionAddOut{}, fmt.Errorf("text is required")
		}
		id, pattern, err := st.AddCriterion(args.TaskID, args.Text)
		if err != nil {
			return nil, criterionAddOut{}, err
		}
		return textResult(fmt.Sprintf("criterion #%d added to task #%d", id, args.TaskID)), criterionAddOut{ID: id, Pattern: pattern}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_criteria_check",
		Description: "Mark an acceptance criterion done (or, with done=false, reopen it).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args criterionCheckArgs) (*sdkmcp.CallToolResult, criterionCheckOut, error) {
		done := args.Done == nil || *args.Done
		if err := st.SetCriterionDone(args.ID, done); err != nil {
			return nil, criterionCheckOut{}, err
		}
		return textResult(fmt.Sprintf("criterion #%d done=%v", args.ID, done)), criterionCheckOut{ID: args.ID, Done: done}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_task_link",
		Description: "Link two tasks (depends_on|blocks|related). Same relation from `acline task link`.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskLinkArgs) (*sdkmcp.CallToolResult, taskLinkOut, error) {
		if _, err := st.GetTask(args.TaskID); err != nil {
			return nil, taskLinkOut{}, err
		}
		if _, err := st.GetTask(args.RelatedTaskID); err != nil {
			return nil, taskLinkOut{}, err
		}
		id, err := st.AddLink(args.TaskID, args.RelatedTaskID, args.Relation)
		if err != nil {
			return nil, taskLinkOut{}, err
		}
		return textResult(fmt.Sprintf("link #%d created: task #%d %s task #%d", id, args.TaskID, args.Relation, args.RelatedTaskID)), taskLinkOut{ID: id}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_task_defer",
		Description: "Mark a task deferred (a disposition independent of status) with a reason and optional " +
			"revisit trigger, or clear it with clear=true.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskDeferArgs) (*sdkmcp.CallToolResult, taskDeferOut, error) {
		if args.Clear {
			if err := st.DeferTask(args.ID, false, "", ""); err != nil {
				return nil, taskDeferOut{}, err
			}
			return textResult(fmt.Sprintf("task #%d deferred flag cleared", args.ID)), taskDeferOut{ID: args.ID, Deferred: false}, nil
		}
		if args.Reason == "" {
			return nil, taskDeferOut{}, fmt.Errorf("reason is required unless clear=true")
		}
		if err := st.DeferTask(args.ID, true, args.Reason, args.Trigger); err != nil {
			return nil, taskDeferOut{}, err
		}
		return textResult(fmt.Sprintf("task #%d deferred", args.ID)), taskDeferOut{ID: args.ID, Deferred: true}, nil
	})
}
