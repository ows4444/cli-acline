package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/redact"
	"acline/internal/store"
)

type decisionOut struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Context   *string `json:"context,omitempty"`
	Decision  *string `json:"decision,omitempty"`
	Rationale *string `json:"rationale,omitempty"`
	ProjectID *int64  `json:"project_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

func toDecisionOut(d store.Decision) decisionOut {
	return decisionOut{
		ID: d.ID, Title: d.Title, Status: d.Status,
		Context: nullStrPtr(d.Context), Decision: nullStrPtr(d.DecisionText), Rationale: nullStrPtr(d.Rationale),
		ProjectID: nullIntPtr(d.ProjectID), CreatedAt: d.CreatedAt,
	}
}

type decisionListArgs struct {
	Status  string `json:"status,omitempty" jsonschema:"proposed|accepted|rejected|deprecated|superseded"`
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type decisionListOut struct {
	Decisions []decisionOut `json:"decisions"`
	Truncated bool          `json:"truncated,omitempty"`
}

type decisionAddArgs struct {
	Title     string `json:"title" jsonschema:"the decision title"`
	Context   string `json:"context,omitempty" jsonschema:"the situation/constraints that led to this decision"`
	Decision  string `json:"decision,omitempty" jsonschema:"what was decided"`
	Rationale string `json:"rationale,omitempty" jsonschema:"why"`
	Scope     string `json:"scope,omitempty"`
	Project   string `json:"project,omitempty" jsonschema:"project name to scope this decision to"`
}

type decisionAddOut struct {
	ID int64 `json:"id"`
}

func registerDecisionTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_decision_list",
		Description: "List ADR-style decisions, optionally filtered by status or project.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args decisionListArgs) (*sdkmcp.CallToolResult, decisionListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, decisionListOut{}, err
		}
		decisions, err := st.ListDecisions(args.Status, projectID)
		if err != nil {
			return nil, decisionListOut{}, err
		}
		page, truncated := paginate(decisions, args.Limit, args.Offset)
		out := decisionListOut{Decisions: make([]decisionOut, len(page)), Truncated: truncated}
		for i, d := range page {
			out.Decisions[i] = toDecisionOut(d)
		}
		return textResult(fmt.Sprintf("%d decision(s)", len(page))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_decision_add",
		Description: "Record a durable/expensive-to-reverse decision (ADR-style). Lands as status=proposed.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args decisionAddArgs) (*sdkmcp.CallToolResult, decisionAddOut, error) {
		if args.Title == "" {
			return nil, decisionAddOut{}, fmt.Errorf("title is required")
		}
		secretFound := redact.Fields(&args.Title, &args.Context, &args.Decision, &args.Rationale)
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, decisionAddOut{}, err
		}
		id, err := st.AddDecision(args.Title, store.DecisionOpts{
			Scope: args.Scope, Context: args.Context, Decision: args.Decision, Rationale: args.Rationale, ProjectID: projectID,
		})
		if err != nil {
			return nil, decisionAddOut{}, err
		}
		st.LogEventGlobal("decision_recorded", fmt.Sprintf("decision #%d recorded", id))
		if secretFound {
			st.LogEventGlobal("secret_redacted", fmt.Sprintf("decision #%d: a pasted secret value was redacted before recording", id))
		}
		return textResult(fmt.Sprintf("decision #%d recorded", id)), decisionAddOut{ID: id}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_decision_accept",
		Description: "Mark a decision accepted (later work is checked against it). A person's decision: refused unless an approval token " +
			"is enabled and presented, and refused for an agent actor without it.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args decisionAcceptArgs) (*sdkmcp.CallToolResult, idOut, error) {
		if err := requireApprovalTokenEnabled(st, "accepting a decision over MCP", "acline decision accept"); err != nil {
			return nil, idOut{}, err
		}
		if err := st.AcceptDecision(args.ID, args.Token); err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("decision #%d accepted", args.ID)), idOut{ID: args.ID}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_decision_reject",
		Description: "Mark a decision rejected. Rejecting an accepted decision is a person's decision: refused unless an " +
			"approval token is enabled and presented, like acline_decision_accept. A proposed one can be rejected freely.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args decisionRetireArgs) (*sdkmcp.CallToolResult, idOut, error) {
		if err := requireTokenToRetire(st, args.ID, "rejecting an accepted decision over MCP", "acline decision reject"); err != nil {
			return nil, idOut{}, err
		}
		if err := st.RejectDecision(args.ID, args.Token); err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("decision #%d rejected", args.ID)), idOut{ID: args.ID}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_decision_supersede",
		Description: "Mark old_id superseded by new_id. Superseding an accepted decision is a person's decision: refused unless an " +
			"approval token is enabled and presented, like acline_decision_accept.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args decisionSupersedeArgs) (*sdkmcp.CallToolResult, decisionSupersedeOut, error) {
		if err := requireTokenToRetire(st, args.OldID, "superseding an accepted decision over MCP", "acline decision supersede"); err != nil {
			return nil, decisionSupersedeOut{}, err
		}
		if err := st.SupersedeDecision(args.OldID, args.NewID, args.Token); err != nil {
			return nil, decisionSupersedeOut{}, err
		}
		return textResult(fmt.Sprintf("decision #%d superseded by #%d", args.OldID, args.NewID)),
			decisionSupersedeOut{OldID: args.OldID, NewID: args.NewID}, nil
	})
}

// idArgs/idOut are shared by the tools in this file whose only input/output is
// a single record id and that need no extra gating -- (memory.go has its own memoryIDArgs/memoryIDOut for the
// same shape, since a memory entry id isn't interchangeable with a
// decision/spec id even though the Go shape is identical).
type idArgs struct {
	ID int64 `json:"id" jsonschema:"the record id"`
}

type idOut struct {
	ID int64 `json:"id"`
}

// decisionAcceptArgs/specApproveArgs add the approval token idArgs deliberately
// does not carry, per requireApprovalTokenEnabled below.
type decisionAcceptArgs struct {
	ID    int64  `json:"id" jsonschema:"the decision id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required, and an approval token must be enabled (see acline auth). Never read from the server's environment."`
}

type specApproveArgs struct {
	ID    int64  `json:"id" jsonschema:"the spec id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required, and an approval token must be enabled (see acline auth). Never read from the server's environment."`
}

// requireApprovalTokenEnabled is the MCP-only extra guard shared by every tool
// that decides a spec, decision or plan: it refuses outright when the store has
// no approval token enabled, even for a human actor, because the MCP server's
// actor defaults to human (see store.ResolveActor) and an agent attached to
// such a server would otherwise approve its own drafted spec/decision/plan with
// no check at all. A task approval (acline_approve) works without a token when
// none is enabled -- that is the older, looser rule this deliberately does not
// use. cmdHint names the CLI fallback for a person with no token enabled.
func requireApprovalTokenEnabled(st *store.Store, action, cmdHint string) error {
	on, err := st.ApprovalTokenEnabled()
	if err != nil {
		return err
	}
	if !on {
		return fmt.Errorf("%s requires an approval token: run `acline auth init` to enable one, or use `%s` in a terminal", action, cmdHint)
	}
	return nil
}

type decisionSupersedeArgs struct {
	OldID int64  `json:"old_id" jsonschema:"the decision being superseded"`
	NewID int64  `json:"new_id" jsonschema:"the decision that supersedes it"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required when old_id is accepted. Never read from the server's environment."`
}

// decisionRetireArgs is idArgs plus the token rejecting an accepted decision needs.
type decisionRetireArgs struct {
	ID    int64  `json:"id" jsonschema:"the decision id"`
	Token string `json:"token,omitempty" jsonschema:"human approval token; required when the decision is accepted. Never read from the server's environment."`
}

// requireTokenToRetire applies requireApprovalTokenEnabled when decision id is
// accepted: retiring one is gated like accepting it (store.ErrAgentCannotRetireDecision).
func requireTokenToRetire(st *store.Store, id int64, action, cmdHint string) error {
	d, err := st.GetDecision(id)
	if err != nil {
		return err
	}
	if d.Status != "accepted" {
		return nil
	}
	return requireApprovalTokenEnabled(st, action, cmdHint)
}

type decisionSupersedeOut struct {
	OldID int64 `json:"old_id"`
	NewID int64 `json:"new_id"`
}

type specOut struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Body      *string `json:"body,omitempty"`
	Status    string  `json:"status"`
	Version   int     `json:"version"`
	ProjectID *int64  `json:"project_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

func toSpecOut(sp store.Spec) specOut {
	return specOut{ID: sp.ID, Title: sp.Title, Body: nullStrPtr(sp.Body), Status: sp.Status, Version: sp.Version, ProjectID: nullIntPtr(sp.ProjectID), CreatedAt: sp.CreatedAt}
}

type specListArgs struct {
	Status  string `json:"status,omitempty" jsonschema:"draft|approved|implemented|superseded"`
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max results (default 100, capped at 500)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip this many results (for paging)"`
}

type specListOut struct {
	Specs     []specOut `json:"specs"`
	Truncated bool      `json:"truncated,omitempty"`
}

type specAddArgs struct {
	Title   string `json:"title" jsonschema:"the spec title"`
	Body    string `json:"body,omitempty"`
	Project string `json:"project,omitempty" jsonschema:"project name to scope this spec to"`
}

type specAddOut struct {
	ID int64 `json:"id"`
}

func registerSpecTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_spec_list",
		Description: "List versioned intent specs that tasks are derived from, optionally filtered by status or project.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args specListArgs) (*sdkmcp.CallToolResult, specListOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, specListOut{}, err
		}
		specs, err := st.ListSpecs(args.Status, projectID)
		if err != nil {
			return nil, specListOut{}, err
		}
		page, truncated := paginate(specs, args.Limit, args.Offset)
		out := specListOut{Specs: make([]specOut, len(page)), Truncated: truncated}
		for i, sp := range page {
			out.Specs[i] = toSpecOut(sp)
		}
		return textResult(fmt.Sprintf("%d spec(s)", len(page))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_spec_add",
		Description: "Add a spec (versioned statement of intent), for non-trivial work a task should derive from. Lands as status=draft.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args specAddArgs) (*sdkmcp.CallToolResult, specAddOut, error) {
		if args.Title == "" {
			return nil, specAddOut{}, fmt.Errorf("title is required")
		}
		secretFound := redact.Fields(&args.Title, &args.Body)
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, specAddOut{}, err
		}
		id, err := st.AddSpec(args.Title, args.Body, store.SpecOpts{ProjectID: projectID})
		if err != nil {
			return nil, specAddOut{}, err
		}
		st.LogEventGlobal("spec_recorded", fmt.Sprintf("spec #%d recorded", id))
		if secretFound {
			st.LogEventGlobal("secret_redacted", fmt.Sprintf("spec #%d: a pasted secret value was redacted before recording", id))
		}
		return textResult(fmt.Sprintf("spec #%d recorded", id)), specAddOut{ID: id}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_spec_approve",
		Description: "Mark a spec approved (ready to derive a plan or tasks from). A person's decision: refused unless an approval token " +
			"is enabled and presented, and refused for an agent actor without it.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args specApproveArgs) (*sdkmcp.CallToolResult, idOut, error) {
		if err := requireApprovalTokenEnabled(st, "approving a spec over MCP", "acline spec approve"); err != nil {
			return nil, idOut{}, err
		}
		if err := st.ApproveSpec(args.ID, args.Token); err != nil {
			return nil, idOut{}, err
		}
		return textResult(fmt.Sprintf("spec #%d approved", args.ID)), idOut{ID: args.ID}, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_spec_revise",
		Description: "Replace a spec's body and bump its version. Revising an approved spec withdraws its approval (it becomes a draft and needs approving again); the earlier text is kept.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args specReviseArgs) (*sdkmcp.CallToolResult, specReviseOut, error) {
		if args.Body == "" {
			return nil, specReviseOut{}, fmt.Errorf("body is required")
		}
		secretFound := redact.Fields(&args.Body)
		version, err := st.ReviseSpec(args.ID, args.Body)
		if err != nil {
			return nil, specReviseOut{}, err
		}
		if secretFound {
			st.LogEventGlobal("secret_redacted", fmt.Sprintf("spec #%d: a pasted secret value was redacted before recording", args.ID))
		}
		return textResult(fmt.Sprintf("spec #%d revised to v%d", args.ID, version)), specReviseOut{ID: args.ID, Version: version}, nil
	})
}

type specReviseArgs struct {
	ID   int64  `json:"id" jsonschema:"the spec id"`
	Body string `json:"body" jsonschema:"the replacement spec text"`
}

type specReviseOut struct {
	ID      int64 `json:"id"`
	Version int   `json:"version"`
}
