package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

// This file is the "dedicated pass" v1 deliberately deferred: task
// completion and approval both carry actor-identity rules (an agent can't
// self-approve; an override is itself an oversight event, not a silent
// bypass) that a rushed first cut would risk getting wrong. Each handler
// below mirrors its CLI counterpart's logic exactly (taskGateCmd/
// taskDoneCmd in cmd/task.go, approveCmd/rejectCmd in cmd/gate.go) rather
// than reimplementing the policy from scratch.

type taskGateArgs struct {
	ID int64 `json:"id" jsonschema:"the task id"`
}

type taskGateOut struct {
	OK       bool     `json:"ok"`
	Blockers []string `json:"blockers,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type taskDoneArgs struct {
	ID    int64  `json:"id" jsonschema:"the task id"`
	Force bool   `json:"force,omitempty" jsonschema:"override an unsatisfied gate (the override is recorded as an approval event, never silent)"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required to force when the store has one enabled (see acline auth). Never read from the server's environment."`
}

type taskDoneOut struct {
	Overridden bool     `json:"overridden"`
	Warnings   []string `json:"warnings,omitempty"`
}

type approveArgs struct {
	ID      int64  `json:"id" jsonschema:"the task id"`
	Kind    string `json:"kind,omitempty" jsonschema:"code_review|override (default code_review)"`
	By      string `json:"by,omitempty" jsonschema:"who approved -- required when the server's actor is an agent, since an agent cannot approve its own work"`
	Note    string `json:"note,omitempty"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE) -- once a project has a can_approve role, the gate requires one"`
	Project string `json:"project,omitempty" jsonschema:"project name to resolve the role in (no ambient project on an MCP server, unlike the CLI)"`
	Token   string `json:"token,omitempty" jsonschema:"human approval token; required when the store has one enabled (see acline auth). Never read from the server's environment."`
}

type approveOut struct {
	ID int64 `json:"id"`
}

type rejectArgs struct {
	ID      int64  `json:"id" jsonschema:"the task id"`
	By      string `json:"by,omitempty" jsonschema:"who rejected"`
	Note    string `json:"note,omitempty" jsonschema:"why it was rejected"`
	Role    string `json:"role,omitempty" jsonschema:"role name (default: $ACLINE_ROLE)"`
	Project string `json:"project,omitempty" jsonschema:"project name to resolve the role in"`
}

type rejectOut struct {
	ID int64 `json:"id"`
}

func registerGateTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_task_gate",
		Description: "Show whether a task can be completed, and what's missing (unrecorded verification, failing checks, missing human approval for high-risk/hitl tasks).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskGateArgs) (*sdkmcp.CallToolResult, taskGateOut, error) {
		gate, err := st.EvaluateGateForTree(args.ID, gateTreeForTask(st, args.ID))
		if err != nil {
			return nil, taskGateOut{}, err
		}
		out := taskGateOut{OK: gate.OK(), Blockers: gate.Blockers, Warnings: gate.Warnings}
		summary := fmt.Sprintf("task #%d: gate satisfied", args.ID)
		if !gate.OK() {
			summary = fmt.Sprintf("task #%d: gate NOT satisfied (%d blocker(s))", args.ID, len(gate.Blockers))
		}
		return textResult(summary), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_task_done",
		Description: "Mark a task done, subject to the verification/approval gate. Without force=true, an " +
			"unsatisfied gate is returned as an error naming each blocker (same as the CLI's `task done` " +
			"without --force). With force=true, the gate is overridden and recorded as an 'override' " +
			"approval event, never silently skipped.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskDoneArgs) (*sdkmcp.CallToolResult, taskDoneOut, error) {
		result, err := st.CompleteTaskForTree(args.ID, args.Force, args.Token, gateTreeForTask(st, args.ID))
		var blocked *store.GateBlockedError
		if errors.As(err, &blocked) {
			return nil, taskDoneOut{}, wrapErr(fmt.Sprintf(
				"cannot complete task: gate not satisfied: %s (retry with force=true to override; the override is recorded)",
				strings.Join(blocked.Blockers, "; ")), err)
		}
		if err != nil {
			return nil, taskDoneOut{}, err
		}
		overridden := result.Overridden
		summary := fmt.Sprintf("task #%d marked done", args.ID)
		if overridden {
			summary = fmt.Sprintf("gate overridden and task #%d marked done (recorded)", args.ID)
		}
		return textResult(summary), taskDoneOut{Overridden: overridden, Warnings: result.Warnings}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_approve",
		Description: "Record human approval of a task (oversight evidence, not an implication). If the " +
			"server's actor is an agent, the approval token is required: naming a person in `by` is not enough -- an agent cannot approve its own work.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args approveArgs) (*sdkmcp.CallToolResult, approveOut, error) {
		aid, err := app.Approve(st, app.ApproveRequest{
			TaskID: args.ID, Kind: args.Kind, By: args.By, Note: args.Note, Token: args.Token,
			RoleArg: args.Role, ProjectArg: args.Project,
		})
		if errors.Is(err, store.ErrAgentCannotApprove) {
			return nil, approveOut{}, fmt.Errorf("%w: a person approves from their own terminal; an agent needs the approval token (`token` argument) — see `acline auth init`", err)
		}
		if err != nil {
			return nil, approveOut{}, err
		}
		return textResult(fmt.Sprintf("approval #%d recorded for task #%d", aid, args.ID)), approveOut{ID: aid}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_reject",
		Description: "Record a rejection at review for a task.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args rejectArgs) (*sdkmcp.CallToolResult, rejectOut, error) {
		aid, err := app.Reject(st, app.RejectRequest{
			TaskID: args.ID, By: args.By, Note: args.Note,
			RoleArg: args.Role, ProjectArg: args.Project,
		})
		if err != nil {
			return nil, rejectOut{}, err
		}
		return textResult(fmt.Sprintf("rejection #%d recorded for task #%d", aid, args.ID)), rejectOut{ID: aid}, nil
	})
}

type taskRouteArgs struct {
	ID      *int64 `json:"id,omitempty" jsonschema:"the task id; omit to pick the next task to work on"`
	Project string `json:"project,omitempty" jsonschema:"restrict the pick to a project name (only when id is omitted)"`
}

type taskRouteOut struct {
	Found         bool             `json:"found"`
	TaskID        int64            `json:"task_id,omitempty"`
	Title         string           `json:"title,omitempty"`
	Status        string           `json:"status,omitempty"`
	Action        string           `json:"action,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	NeedsHuman    bool             `json:"needs_human,omitempty"`
	Role          *string          `json:"role,omitempty"`
	SpecID        *int64           `json:"spec_id,omitempty"`
	WaitingOn     []string         `json:"waiting_on,omitempty"`
	Blockers      []string         `json:"blockers,omitempty"`
	OpenCriteria  []string         `json:"open_criteria,omitempty"`
	FailingChecks []string         `json:"failing_checks,omitempty"`
	Lessons       []memoryEntryOut `json:"lessons,omitempty"`
}

func registerRouteTool(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_task_route",
		Description: "Say what should happen next for a task and which role should do it, derived from its recorded state " +
			"(spec, criteria, checks, gate). Omit id to pick the highest-priority task an agent can act on. Advisory only.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args taskRouteArgs) (*sdkmcp.CallToolResult, taskRouteOut, error) {
		var r *store.Route
		var err error
		if args.ID != nil {
			r, err = st.RouteTask(*args.ID)
		} else {
			var projectID *int64
			if projectID, err = resolveProject(st, args.Project); err != nil {
				return nil, taskRouteOut{}, err
			}
			r, err = st.NextTask(projectID)
		}
		if err != nil {
			return nil, taskRouteOut{}, err
		}
		if r == nil {
			return textResult("no open tasks"), taskRouteOut{}, nil
		}
		out := taskRouteOut{
			Found: true, TaskID: r.TaskID, Title: r.Title, Status: r.Status, Action: r.Action, Reason: r.Reason,
			NeedsHuman: r.NeedsHuman, SpecID: r.SpecID, WaitingOn: r.WaitingOn, Blockers: r.Blockers, OpenCriteria: r.OpenCriteria,
			FailingChecks: r.FailingChecks,
		}
		if r.Role != nil {
			out.Role = &r.Role.Name
		}
		for _, m := range r.Lessons {
			out.Lessons = append(out.Lessons, toMemoryEntryOut(m))
		}
		who := "any agent"
		if r.Role != nil {
			who = "role " + r.Role.Name
		}
		if r.NeedsHuman {
			who = "a human (" + who + ")"
		}
		return textResult(fmt.Sprintf("task #%d: next is %s (%s) — %s", r.TaskID, r.Action, who, r.Reason)), out, nil
	})
}
