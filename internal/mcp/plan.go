package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/store"
)

// Plans over MCP: anyone may read and PROPOSE. Accepting a plan turns it into
// real, launchable tasks, which is a person's decision (docs/ARCHITECTURE.md, "Authority"), so the
// tools that decide one are stricter than a plain approve tool:
//
//   - acline_plan_approve and acline_plan_edit_item REFUSE unless an approval
//     token is enabled, and then require it as a per-call argument (see
//     requireApprovalTokenEnabled in decision_spec.go, shared with
//     acline_spec_approve/acline_decision_accept for the same reason: a task
//     approval over MCP works without a token when none is enabled, but a spec,
//     decision or plan approval does not, because any agent attached to a
//     default (human-actor) server could otherwise approve its own proposal.
//     Without a token, a person approves from the CLI instead.
//   - the store additionally refuses an agent actor that does not hold the token.
//   - acline_plan_reject needs no token (the safe direction), only a non-agent actor.

type planItemArgs struct {
	Ref         string   `json:"ref" jsonschema:"a short key for this item, unique in the plan (1-20 letters, digits, '.', '_' or '-'), e.g. T1; other items refer to it"`
	Title       string   `json:"title" jsonschema:"the task title (1-200 characters)"`
	Description string   `json:"description,omitempty"`
	Area        string   `json:"area,omitempty" jsonschema:"ownership area, e.g. store|cli|docs"`
	Type        string   `json:"type,omitempty" jsonschema:"bug|refactor|test|architecture|security|performance|reliability|contract|database|messaging|ui_ux|accessibility|feature|debt|docs|devex"`
	Risk        string   `json:"risk,omitempty" jsonschema:"low|medium|high|critical (default low); high and critical need a person to approve completion"`
	Autonomy    string   `json:"autonomy,omitempty" jsonschema:"hitl|hotl (default hotl). A plan can never grant auto"`
	Parent      string   `json:"parent,omitempty" jsonschema:"ref of the parent item (grouping only; a parent does not wait for its children)"`
	Milestone   string   `json:"milestone,omitempty" jsonschema:"milestone name, created if it does not exist"`
	Size        string   `json:"size,omitempty" jsonschema:"S|M|L"`
	DependsOn   []string `json:"depends_on,omitempty" jsonschema:"refs of items that must be done before this one; must not form a cycle"`
	Criteria    []string `json:"criteria,omitempty" jsonschema:"acceptance criteria, EARS phrasing: When X, the system shall Y (at most 20)"`
}

type planProposeArgs struct {
	SpecID int64          `json:"spec_id" jsonschema:"an APPROVED spec to plan"`
	Note   string         `json:"note,omitempty" jsonschema:"why this breakdown, for the reviewer"`
	Items  []planItemArgs `json:"items" jsonschema:"the proposed tasks (1 to 30)"`
}

type planProposeOut struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Items  int    `json:"items"`
	Next   string `json:"next"`
}

type planItemOut struct {
	Ref         string   `json:"ref"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Area        *string  `json:"area,omitempty"`
	Type        *string  `json:"type,omitempty"`
	Risk        string   `json:"risk"`
	Autonomy    string   `json:"autonomy"`
	Parent      *string  `json:"parent,omitempty"`
	Milestone   *string  `json:"milestone,omitempty"`
	Size        *string  `json:"size,omitempty"`
	Dropped     bool     `json:"dropped,omitempty"`
	TaskID      *int64   `json:"task_id,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Criteria    []string `json:"criteria,omitempty"`
}

type planOut struct {
	ID          int64   `json:"id"`
	SpecID      int64   `json:"spec_id"`
	SpecVersion int     `json:"spec_version"`
	Status      string  `json:"status"`
	Version     int     `json:"version"`
	Note        *string `json:"note,omitempty"`
	ProposedBy  *string `json:"proposed_by,omitempty"`
	CreatedAt   string  `json:"created_at"`
	DecidedAt   *string `json:"decided_at,omitempty"`
	// Stale is true for a draft whose spec changed since it was proposed: it
	// can no longer be approved and must be revised.
	Stale bool `json:"stale,omitempty"`
}

type planListArgs struct {
	SpecID  *int64 `json:"spec_id,omitempty" jsonschema:"only this spec's plans"`
	Status  string `json:"status,omitempty" jsonschema:"draft|approved|rejected|superseded"`
	Project string `json:"project,omitempty" jsonschema:"only plans for this project's specs"`
}

type planListOut struct {
	Plans []planOut `json:"plans"`
}

type planShowArgs struct {
	ID int64 `json:"id" jsonschema:"the plan id"`
}

type planShowOut struct {
	Plan  planOut       `json:"plan"`
	Items []planItemOut `json:"items"`
	// WouldCreate is how many tasks approving the plan would create now.
	WouldCreate int `json:"would_create"`
}

func toPlanOut(st *store.Store, p store.Plan) planOut {
	out := planOut{
		ID: p.ID, SpecID: p.SpecID, SpecVersion: p.SpecVersion, Status: p.Status, Version: p.Version,
		Note: nullStrPtr(p.Note), CreatedAt: p.CreatedAt, DecidedAt: nullStrPtr(p.DecidedAt), Stale: st.PlanIsStale(&p),
	}
	if p.ActorID.Valid {
		by := p.ActorType.String + "/" + p.ActorID.String
		out.ProposedBy = &by
	}
	return out
}

type planApproveArgs struct {
	ID    int64  `json:"id" jsonschema:"the plan id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required, and an approval token must be enabled (see acline auth). Never read from the server's environment."`
}

type createdTaskOut struct {
	Ref    string `json:"ref"`
	TaskID int64  `json:"task_id"`
}

type planApproveOut struct {
	ID      int64            `json:"id"`
	Created []createdTaskOut `json:"created"`
}

type planEditItemArgs struct {
	ID        int64   `json:"id" jsonschema:"the plan id (must be a draft)"`
	Ref       string  `json:"ref" jsonschema:"the item's ref, e.g. T1"`
	Title     *string `json:"title,omitempty"`
	Area      *string `json:"area,omitempty"`
	Risk      *string `json:"risk,omitempty" jsonschema:"low|medium|high|critical"`
	Autonomy  *string `json:"autonomy,omitempty" jsonschema:"hitl|hotl (a plan cannot grant auto)"`
	Size      *string `json:"size,omitempty" jsonschema:"S|M|L, or empty to clear"`
	Milestone *string `json:"milestone,omitempty" jsonschema:"milestone name, or empty to clear"`
	Drop      *bool   `json:"drop,omitempty" jsonschema:"true leaves the item out of what approval creates; false puts it back"`
	Token     string  `json:"token,omitempty" jsonschema:"human approval token; required, and an approval token must be enabled. Never read from the server's environment."`
}

type planItemEditOut struct {
	ID  int64  `json:"id"`
	Ref string `json:"ref"`
}

type planRejectArgs struct {
	ID   int64  `json:"id" jsonschema:"the plan id (must be a draft)"`
	Note string `json:"note,omitempty" jsonschema:"why; kept as a note for acline reflect"`
}

type planRejectOut struct {
	ID int64 `json:"id"`
}

// requirePlanToken is requireApprovalTokenEnabled worded for plan decisions.
func requirePlanToken(st *store.Store) error {
	return requireApprovalTokenEnabled(st, "deciding a plan over MCP", "acline plan approve")
}

func registerPlanTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name: "acline_plan_propose",
		Description: "Propose a task graph (a plan) for an approved spec. This only records a DRAFT: nothing becomes a task, and nothing can be " +
			"routed or launched, until a person approves it (`acline plan approve`). There is no tool to approve, edit or reject a plan. " +
			"Items refer to each other by ref; dependencies must not form a cycle; a plan cannot grant autonomy 'auto'; at most 30 items. " +
			"A spec can have one draft at a time.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args planProposeArgs) (*sdkmcp.CallToolResult, planProposeOut, error) {
		in := store.PlanInput{Note: args.Note}
		for _, it := range args.Items {
			in.Items = append(in.Items, store.PlanItemInput{
				Ref: it.Ref, Title: it.Title, Description: it.Description, Area: it.Area, Type: it.Type, Risk: it.Risk,
				Autonomy: it.Autonomy, Parent: it.Parent, Milestone: it.Milestone, Size: it.Size,
				DependsOn: it.DependsOn, Criteria: it.Criteria,
			})
		}
		id, err := st.ProposePlan(args.SpecID, in)
		if err != nil {
			return nil, planProposeOut{}, err
		}
		out := planProposeOut{ID: id, Status: "draft", Items: len(in.Items), Next: "a person reviews it with `acline plan show " + fmt.Sprint(id) + "` and approves or rejects it"}
		return textResult(fmt.Sprintf("plan #%d proposed for spec #%d (%d items); it is a draft until a person approves it", id, args.SpecID, len(in.Items))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_plan_list",
		Description: "List plans, newest first, optionally for one spec and/or status. Read-only.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args planListArgs) (*sdkmcp.CallToolResult, planListOut, error) {
		pid, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, planListOut{}, err
		}
		plans, err := st.ListProjectPlans(args.SpecID, args.Status, pid)
		if err != nil {
			return nil, planListOut{}, err
		}
		out := planListOut{Plans: make([]planOut, len(plans))}
		for i, p := range plans {
			out.Plans[i] = toPlanOut(st, p)
		}
		return textResult(fmt.Sprintf("%d plan(s)", len(plans))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_plan_show",
		Description: "Show a plan: its items, dependencies, criteria, and how many tasks approving it would create. Read-only.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args planShowArgs) (*sdkmcp.CallToolResult, planShowOut, error) {
		p, err := st.GetPlan(args.ID)
		if err != nil {
			return nil, planShowOut{}, err
		}
		items, err := st.PlanItems(args.ID)
		if err != nil {
			return nil, planShowOut{}, err
		}
		out := planShowOut{Plan: toPlanOut(st, *p), Items: make([]planItemOut, len(items))}
		for i, it := range items {
			out.Items[i] = planItemOut{
				Ref: it.Ref, Title: it.Title, Description: it.Description, Area: nullStrPtr(it.Area), Type: nullStrPtr(it.Type),
				Risk: it.Risk, Autonomy: it.Autonomy, Parent: nullStrPtr(it.Parent), Milestone: nullStrPtr(it.Milestone),
				Size: nullStrPtr(it.Size), Dropped: it.Dropped, TaskID: nullIntPtr(it.TaskID), DependsOn: it.DependsOn, Criteria: it.Criteria,
			}
			if !it.Dropped {
				out.WouldCreate++
			}
		}
		return textResult(fmt.Sprintf("plan #%d [%s]: %d item(s)", p.ID, p.Status, len(items))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_plan_approve",
		Description: "Approve a DRAFT plan: create its tasks, criteria, dependency links, parents and milestones in one transaction. A person's decision: " +
			"refused unless an approval token is enabled and presented, and refused for an agent actor without it. Refused for a stale plan (its spec changed).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args planApproveArgs) (*sdkmcp.CallToolResult, planApproveOut, error) {
		if err := requirePlanToken(st); err != nil {
			return nil, planApproveOut{}, err
		}
		created, err := st.ApprovePlan(args.ID, args.Token)
		if err != nil {
			return nil, planApproveOut{}, err
		}
		out := planApproveOut{ID: args.ID, Created: make([]createdTaskOut, len(created))}
		for i, c := range created {
			out.Created[i] = createdTaskOut{Ref: c.Ref, TaskID: c.TaskID}
		}
		return textResult(fmt.Sprintf("plan #%d approved: %d task(s) created", args.ID, len(created))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_plan_edit_item",
		Description: "Change or drop one item of a DRAFT plan before it is approved. A person's decision, gated exactly like acline_plan_approve " +
			"(an approval token must be enabled and presented).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args planEditItemArgs) (*sdkmcp.CallToolResult, planItemEditOut, error) {
		if err := requirePlanToken(st); err != nil {
			return nil, planItemEditOut{}, err
		}
		err := st.EditPlanItem(args.ID, args.Ref, store.PlanItemEdit{
			Title: args.Title, Area: args.Area, Risk: args.Risk, Autonomy: args.Autonomy, Size: args.Size, Milestone: args.Milestone, Drop: args.Drop,
		}, args.Token)
		if err != nil {
			return nil, planItemEditOut{}, err
		}
		return textResult(fmt.Sprintf("plan #%d item %s updated", args.ID, args.Ref)), planItemEditOut{ID: args.ID, Ref: args.Ref}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_plan_reject",
		Description: "Reject a DRAFT plan. Needs no token (rejecting is the safe direction) but is refused for an agent actor. The note is kept for acline reflect.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args planRejectArgs) (*sdkmcp.CallToolResult, planRejectOut, error) {
		if err := st.RejectPlan(args.ID, args.Note); err != nil {
			return nil, planRejectOut{}, err
		}
		return textResult(fmt.Sprintf("plan #%d rejected", args.ID)), planRejectOut{ID: args.ID}, nil
	})
}
