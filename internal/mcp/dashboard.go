// dashboard.go closes MCP tool coverage for the CLI's read-only
// reporting/governance surface: an MCP client would otherwise have
// no way to see the session/dashboard bootstrap view, the verification-tax
// metrics, the audit-chain integrity check, or the list of registered
// projects without dropping to a terminal. `project use` is deliberately
// left out — it writes a `.acline-project` marker file relative to a CLI
// invocation's cwd, a concept a long-running MCP server has no analog for
// (see resolveProject's doc comment in server.go).
package mcp

import (
	"context"
	"errors"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"acline/internal/app"
	"acline/internal/store"
)

type dashboardArgs struct {
	Project string `json:"project,omitempty" jsonschema:"restrict to a project name"`
}

type dashboardOut struct {
	ActiveSession          *sessionOut      `json:"active_session,omitempty"`
	Actor                  string           `json:"actor"`
	TasksTotal             int              `json:"tasks_total"`
	TasksNeedingAttention  []taskOut        `json:"tasks_needing_attention,omitempty"`
	ApprovedSpecs          []specOut        `json:"approved_specs,omitempty"`
	AcceptedDecisions      []decisionOut    `json:"accepted_decisions,omitempty"`
	Memory                 []memoryEntryOut `json:"memory,omitempty"`
	SpecsAwaitingPlanCount int              `json:"specs_awaiting_plan_count"`
	DraftPlansCount        int              `json:"draft_plans_count"`
	PendingMemoryCount     int              `json:"pending_memory_count"`
	DraftedMemoryCount     int              `json:"drafted_memory_count"`
	DecayingMemoryCount    int              `json:"decaying_memory_count"`
	UnverifiedDepsCount    int              `json:"unverified_deps_count"`
}

type sessionOut struct {
	ID        int64  `json:"id"`
	StartedAt string `json:"started_at"`
	TaskID    *int64 `json:"task_id,omitempty"`
	ProjectID *int64 `json:"project_id,omitempty"`
}

type metricsArgs struct {
	Project string `json:"project,omitempty" jsonschema:"only this project's tasks, checks, sessions, memory and events"`
}

type metricsOut struct {
	TasksTotal       int            `json:"tasks_total"`
	TasksDone        int            `json:"tasks_done"`
	TasksByActor     map[string]int `json:"tasks_by_actor,omitempty"`
	ChecksTotal      int            `json:"checks_total"`
	ChecksFailed     int            `json:"checks_failed"`
	ApprovalsTotal   int            `json:"approvals_total"`
	Overrides        int            `json:"overrides"`
	Reworked         int            `json:"reworked"`
	UnverifiedDeps   int            `json:"unverified_deps"`
	PendingMemory    int            `json:"pending_memory"`
	EventsByActor    map[string]int `json:"events_by_actor,omitempty"`
	TokensIn         int64          `json:"tokens_in"`
	TokensOut        int64          `json:"tokens_out"`
	CostUSD          float64        `json:"cost_usd"`
	Sessions         int            `json:"sessions"`
	PolicyViolations int            `json:"policy_violations"`
	GuardDenials     int            `json:"guard_denials"`
}

type verifyArgs struct{}

type verifyOut struct {
	OK       bool   `json:"ok"`
	Checked  int    `json:"checked"`
	BadID    int64  `json:"bad_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Unhashed int    `json:"unhashed"`
	// Sealed approvals/checks (see store/seal.go): how many matched their seal,
	// how many predate sealing, and the first tampered record if any. Both
	// ts:"optional" since they're absent on servers older than acline 0.2.
	RecordsChecked  int    `json:"records_checked" ts:"optional"`
	RecordsUnsealed int    `json:"records_unsealed" ts:"optional"`
	BadRecord       string `json:"bad_record,omitempty"`
}

type projectListArgs struct{}

type projectOut struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path,omitempty"`
	AutonomyDefault string `json:"autonomy_default"`
}

type projectListOut struct {
	Projects []projectOut `json:"projects"`
}

type projectResolveArgs struct {
	Path string `json:"path" jsonschema:"an absolute filesystem path (e.g. a VS Code workspace folder) to resolve to a registered project"`
}

type projectResolveOut struct {
	Found   bool   `json:"found"`
	Project string `json:"project,omitempty"`
}

func registerDashboardTools(s *sdkmcp.Server, st *store.Store) {
	addTool(s, &sdkmcp.Tool{
		Name:        "acline_dashboard",
		Description: "Bootstrap view for a new session: active session, open tasks needing attention, approved specs, accepted decisions, memory, and pending-review nudges. Same view `acline dashboard` prints.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args dashboardArgs) (*sdkmcp.CallToolResult, dashboardOut, error) {
		projectID, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, dashboardOut{}, err
		}

		d, err := app.Dashboard(st, projectID)
		if err != nil {
			return nil, dashboardOut{}, err
		}

		var out dashboardOut
		out.Actor = fmt.Sprintf("%s/%s", st.Actor.Type, st.Actor.ID)
		if d.Session != nil {
			sess := d.Session
			out.ActiveSession = &sessionOut{ID: sess.ID, StartedAt: sess.StartedAt, TaskID: nullIntPtr(sess.TaskID), ProjectID: nullIntPtr(sess.ProjectID)}
		}

		out.TasksTotal = len(d.Tasks)
		for _, t := range d.TasksNeedingAttention {
			out.TasksNeedingAttention = append(out.TasksNeedingAttention, toTaskOut(t))
		}
		for _, sp := range d.ApprovedSpecs {
			out.ApprovedSpecs = append(out.ApprovedSpecs, toSpecOut(sp))
		}
		for _, dec := range d.AcceptedDecisions {
			out.AcceptedDecisions = append(out.AcceptedDecisions, toDecisionOut(dec))
		}
		for _, m := range d.Memory {
			out.Memory = append(out.Memory, toMemoryEntryOut(m))
		}
		out.SpecsAwaitingPlanCount = len(d.SpecsAwaitingPlan)
		out.DraftPlansCount = len(d.DraftPlans)
		out.PendingMemoryCount = d.PendingMemoryCount
		out.DraftedMemoryCount = d.DraftedMemoryCount
		out.DecayingMemoryCount = d.DecayingMemoryCount
		out.UnverifiedDepsCount = len(d.UnverifiedDeps)

		return textResult(fmt.Sprintf("%d open task(s), %d needing attention", out.TasksTotal, len(out.TasksNeedingAttention))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_metrics",
		Description: "Verification-tax view: what was produced (tasks, sessions) and what checking it took (checks, failures, approvals, overrides, rework, policy violations, guard denials, token/cost totals).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args metricsArgs) (*sdkmcp.CallToolResult, metricsOut, error) {
		pid, err := resolveProject(st, args.Project)
		if err != nil {
			return nil, metricsOut{}, err
		}
		m, err := st.ComputeMetricsFor(pid)
		if err != nil {
			return nil, metricsOut{}, err
		}
		out := metricsOut{
			TasksTotal: m.TasksTotal, TasksDone: m.TasksDone, TasksByActor: m.TasksByActor,
			ChecksTotal: m.ChecksTotal, ChecksFailed: m.ChecksFailed, ApprovalsTotal: m.ApprovalsTotal,
			Overrides: m.Overrides, Reworked: m.Reworked, UnverifiedDeps: m.UnverifiedDeps,
			PendingMemory: m.PendingMemory, EventsByActor: m.EventsByActor,
			TokensIn: m.TokensIn, TokensOut: m.TokensOut, CostUSD: m.CostUSD, Sessions: m.Sessions,
			PolicyViolations: m.PolicyViolations, GuardDenials: m.GuardDenials,
		}
		return textResult(fmt.Sprintf("%d tasks total (%d done), %d checks (%d failed)", out.TasksTotal, out.TasksDone, out.ChecksTotal, out.ChecksFailed)), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_verify",
		Description: "Verify the audit trail's hash chain is intact -- detects edits, deletions, or insertions made outside normal recording, including direct writes to the database file.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args verifyArgs) (*sdkmcp.CallToolResult, verifyOut, error) {
		r, err := st.VerifyChain()
		if err != nil {
			return nil, verifyOut{}, err
		}
		rec, err := st.VerifyRecords()
		if err != nil {
			return nil, verifyOut{}, err
		}
		out := verifyOut{
			OK: r.OK() && rec.OK(), Checked: r.Checked, BadID: r.BadID, Reason: r.Reason, Unhashed: r.Unhashed,
			RecordsChecked: rec.Checked, RecordsUnsealed: rec.Unsealed, BadRecord: rec.BadRef,
		}
		if !rec.OK() && r.OK() {
			out.Reason = rec.Reason
		}
		switch {
		case !r.OK():
			return textResult(fmt.Sprintf("audit trail FAILED verification at event #%d: %s", r.BadID, r.Reason)), out, nil
		case !rec.OK():
			return textResult(fmt.Sprintf("approvals/checks FAILED verification at %s: %s", rec.BadRef, rec.Reason)), out, nil
		}
		return textResult(fmt.Sprintf("audit trail intact: %d event(s), %d sealed record(s) verified", r.Checked, rec.Checked)), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name:        "acline_project_list",
		Description: "List registered projects (name, path, default autonomy).",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args projectListArgs) (*sdkmcp.CallToolResult, projectListOut, error) {
		projects, err := st.ListProjects()
		if err != nil {
			return nil, projectListOut{}, err
		}
		out := projectListOut{Projects: make([]projectOut, len(projects))}
		for i, p := range projects {
			out.Projects[i] = projectOut{ID: p.ID, Name: p.Name, Path: p.Path.String, AutonomyDefault: p.AutonomyDefault}
		}
		return textResult(fmt.Sprintf("%d project(s) registered", len(projects))), out, nil
	})

	addTool(s, &sdkmcp.Tool{
		Name: "acline_project_resolve",
		Description: "Resolve a filesystem path to its registered project, the same way the CLI resolves its " +
			"own cwd (a .acline-project marker file walking up from path, then path or an ancestor matching a " +
			"registered project's path -- case-sensitive, same as the CLI). Reports found=false rather than " +
			"erroring when nothing matches.",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, args projectResolveArgs) (*sdkmcp.CallToolResult, projectResolveOut, error) {
		p, err := st.ResolveProjectForPath(args.Path)
		if errors.Is(err, store.ErrNoProject) {
			return textResult("no project matches"), projectResolveOut{Found: false}, nil
		}
		if err != nil {
			return nil, projectResolveOut{}, err
		}
		return textResult(fmt.Sprintf("resolved to project %q", p.Name)), projectResolveOut{Found: true, Project: p.Name}, nil
	})
}
