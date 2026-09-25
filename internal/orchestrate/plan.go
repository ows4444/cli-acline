package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"acline/internal/clip"
	"acline/internal/scaffold"
	"acline/internal/store"
	"acline/internal/untrusted"
)

// A planning run asks one bounded agent to PROPOSE a task graph for an approved
// spec. The agent is read-only: it may read the codebase and submit exactly one
// thing, `acline plan propose`. It cannot edit files, and it cannot approve,
// edit or reject a plan (the store refuses an agent for those regardless of
// what the tool lists say). What it produces is a draft that waits for a person.

const (
	StopPlanExists   Stop = "plan_exists"
	StopNotPlannable Stop = "not_plannable"
)

const (
	maxPromptSpec      = 8000
	maxPromptTasks     = 50
	maxPromptDecisions = 20
	maxPromptLessons   = 20
)

// PlanReport describes one planning run.
type PlanReport struct {
	SpecID   int64
	Launched bool
	Stop     Stop
	Detail   string
	PlanID   int64 // the draft the agent proposed, when it did
	ExitCode int
	TimedOut bool
	Duration time.Duration
	CostUSD  float64
	Summary  string
	Denied   []string
	Command  []string // dry run: the agent command line that would run
	Prompt   string   // dry run: the prompt that would be sent
}

// PlanTools returns the allow and deny lists for a planning run. The allow list
// is read tools plus the one submit command; the deny list adds every way an
// agent could change files or decide a plan, on top of the standing acline
// denials.
func PlanTools(extra []string) (allow, deny []string) {
	allow = append([]string{"Read", "Grep", "Glob", "Bash(acline plan propose *)"}, extra...)
	_, base := Tools(store.RouteVerify, nil)
	deny = append(base,
		"Edit", "Write", "NotebookEdit",
		"Bash(acline plan approve*)", "Bash(acline plan edit*)", "Bash(acline plan reject*)", "Bash(acline plan revise*)",
	)
	return allow, deny
}

// PlanPrompt assembles what the planning agent is told: the approved spec, the
// context that keeps it from duplicating or contradicting existing work, the
// architect role contract, the plan format and rules, and how to submit.
func PlanPrompt(st *store.Store, spec *store.Spec) (string, error) {
	var projectID *int64
	if spec.ProjectID.Valid {
		projectID = &spec.ProjectID.Int64
	}
	var w strings.Builder
	fmt.Fprintf(&w, "# Propose a plan for spec #%d: %s\n\n", spec.ID, oneLine(spec.Title))
	w.WriteString(untrusted.Rule + "\n\n")
	w.WriteString("You are proposing a breakdown of an APPROVED spec into tasks. You are read-only: read the codebase to understand it, " +
		"then submit exactly one proposal. A person reviews it and decides; nothing you propose can run until they approve it. " +
		"Do not edit any file, and do not try to approve, edit or reject a plan.\n\n")

	fmt.Fprintf(&w, "## The spec (v%d, %s)\n\n%s\n", spec.Version, spec.Status, untrusted.Quote("Spec text", truncateText(spec.Body.String, maxPromptSpec, "acline spec show "+fmt.Sprint(spec.ID))))

	if decisions, err := st.ListDecisions("accepted", projectID); err == nil && len(decisions) > 0 {
		w.WriteString("\n## Accepted decisions (do not contradict these)\n\n")
		var b strings.Builder
		for i, d := range decisions {
			if i >= maxPromptDecisions {
				fmt.Fprintf(&b, "- … and %d more (acline decision list)\n", len(decisions)-i)
				break
			}
			fmt.Fprintf(&b, "- #%d %s: %s\n", d.ID, d.Title, oneLine(d.DecisionText.String))
		}
		w.WriteString(untrusted.Quote("Accepted decisions", b.String()) + "\n")
	}
	if tasks, err := st.ListTasks(store.TaskFilter{ProjectID: projectID}); err == nil && len(tasks) > 0 {
		w.WriteString("\n## Open tasks that already exist (do not duplicate them)\n\n")
		var b strings.Builder
		for i, t := range tasks {
			if i >= maxPromptTasks {
				fmt.Fprintf(&b, "- … and %d more (acline task list)\n", len(tasks)-i)
				break
			}
			fmt.Fprintf(&b, "- #%d %s [%s]\n", t.ID, t.Title, t.Status)
		}
		w.WriteString(untrusted.Quote("Existing tasks", b.String()) + "\n")
	}
	if mem, err := st.ListMemory(store.MemoryFilter{Status: "approved", ProjectID: projectID}); err == nil && len(mem) > 0 {
		w.WriteString("\n## Lessons from earlier work (approved)\n\n")
		var b strings.Builder
		for i, m := range mem {
			if i >= maxPromptLessons {
				break
			}
			fmt.Fprintf(&b, "- [%s] %s\n", m.Kind, oneLine(m.Body))
		}
		w.WriteString(untrusted.Quote("Lessons", b.String()) + "\n")
	}
	if c, ok := scaffold.RoleContract("architect"); ok {
		w.WriteString("\n## Role contract: architect\n\n" + demote(c) + "\n")
	}

	w.WriteString(`
## How to plan

- One task is one reviewable change: small enough to finish and verify on its own. Aim for roughly 5 to 15 items; the limit is 30.
- Give each item acceptance criteria in EARS form ("When X, the system shall Y"), so it can be verified with a real check.
- Add a dependency only when an item truly cannot start before another is done. Do not chain items just to impose an order.
- Set risk honestly: anything touching security, migrations, data loss, auth or money is medium or higher. You may set autonomy hitl or hotl; you cannot grant auto.
- Group related items under a parent (grouping only). Name a milestone when the spec implies a delivery point.
- Use the codebase to be specific: name the areas and files a task touches. Do not invent parts of the system that are not there.

## The plan format (JSON)

` + "```json" + `
{"note": "why this breakdown",
 "items": [
  {"ref": "T1", "title": "Add the stock table", "description": "…", "area": "store", "type": "database",
   "risk": "medium", "autonomy": "hotl", "size": "S", "milestone": "v1",
   "criteria": ["When a stock row is written, the system shall persist it"]},
  {"ref": "T2", "title": "Expose stock over the CLI", "area": "cli", "depends_on": ["T1"], "parent": "T1",
   "criteria": ["When stock is queried, the system shall return the current level"]}
 ]}
` + "```" + `

Fields: ref (1-20 letters, digits, . _ -), title, description, area, type, risk (low|medium|high|critical), autonomy (hitl|hotl), parent, milestone, size (S|M|L), depends_on (refs), criteria.

## Submit

Run exactly this, with your JSON in place of the example (the heredoc keeps it out of any file):

` + "```" + `
acline plan propose ` + fmt.Sprint(spec.ID) + ` --file - <<'PLAN'
{ ...your plan... }
PLAN
` + "```" + `

If it is rejected, read the error, fix the plan, and run it again. Submit once it succeeds, then stop and summarize the plan in a few lines.
`)
	return w.String(), nil
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

func truncateText(s string, n int, more string) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return clip.Bytes(s, n) + fmt.Sprintf("\n\n… (truncated; full text: `%s`)", more)
}

// demote pushes an embedded document's headings below the prompt's own.
func demote(s string) string {
	lines := strings.Split(s, "\n")
	fence := false
	for i, l := range lines {
		if strings.HasPrefix(l, "```") {
			fence = !fence
		}
		if !fence && strings.HasPrefix(l, "#") {
			lines[i] = "##" + l
		}
	}
	return strings.Join(lines, "\n")
}

// PlanOptions bound a planning run. Like Options, the limits are the caller's
// to set.
type PlanOptions struct {
	SpecID        int64
	StepBudgetUSD float64
	StepTimeout   time.Duration
	Dir           string
	ExtraAllow    []string
	PassEnv       []string // extra environment variable names the agent may inherit
	StopFile      string
	DryRun        bool
	NoSandbox     bool // launch without the Bash sandbox (see sandboxSettings)
}

// Plan asks an agent to propose a plan for one approved spec. st must carry an
// agent actor. It never approves, and it launches only for a spec that is
// genuinely waiting for a plan (approved, no live plan, no tasks).
func Plan(ctx context.Context, st *store.Store, agent Agent, opts PlanOptions) (PlanReport, error) {
	rep := PlanReport{SpecID: opts.SpecID}
	if st.Actor.Type != "agent" {
		return rep, errors.New("orchestrator must run with an agent actor")
	}
	spec, err := st.GetSpec(opts.SpecID)
	if err != nil {
		return rep, err
	}
	if spec.Status != "approved" {
		rep.Stop, rep.Detail = StopNotPlannable, fmt.Sprintf("spec #%d is %s: only an approved spec can be planned", spec.ID, spec.Status)
		return rep, nil
	}
	if drafts, _ := st.ListPlans(&spec.ID, "draft"); len(drafts) > 0 {
		rep.Stop, rep.Detail = StopPlanExists, fmt.Sprintf("spec #%d already has draft plan #%d: review it (acline plan show %d), or reject it first", spec.ID, drafts[0].ID, drafts[0].ID)
		return rep, nil
	}
	awaiting, err := st.SpecsAwaitingPlan(nil)
	if err != nil {
		return rep, err
	}
	waiting := false
	for _, a := range awaiting {
		waiting = waiting || a.ID == spec.ID
	}
	if !waiting {
		rep.Stop, rep.Detail = StopNotPlannable, fmt.Sprintf("spec #%d already has an approved plan or tasks; planning it again would duplicate work", spec.ID)
		return rep, nil
	}
	if opts.StopFile != "" {
		if _, err := os.Stat(opts.StopFile); err == nil {
			rep.Stop, rep.Detail = StopKilled, "stop file present: "+opts.StopFile
			return rep, nil
		}
	}
	if _, err := st.CurrentSession(); err == nil {
		rep.Stop, rep.Detail = StopSessionActive, "a session is already active; end it first"
		return rep, nil
	}

	prompt, err := PlanPrompt(st, spec)
	if err != nil {
		return rep, err
	}
	allow, deny := PlanTools(opts.ExtraAllow)
	req := launch{Dir: opts.Dir, PassEnv: opts.PassEnv, NoSandbox: opts.NoSandbox, StepBudgetUSD: opts.StepBudgetUSD, StepTimeout: opts.StepTimeout}.request(st.Path, "architect", prompt, allow, deny)
	if opts.DryRun {
		rep.Stop, rep.Detail, rep.Prompt = StopDryRun, "not launched", prompt
		if c, ok := agent.(ClaudeAgent); ok {
			rep.Command = append([]string{c.bin()}, c.Args(req)...)
		}
		return rep, nil
	}

	var projectID *int64
	if spec.ProjectID.Valid {
		projectID = &spec.ProjectID.Int64
	}
	start := store.SessionStart{ProjectID: projectID, Policy: store.Policy{Label: fmt.Sprintf("orchestrator: plan spec #%d", spec.ID)}}
	if role, rerr := st.GetRoleByName(projectID, "architect"); rerr == nil {
		start.RoleID = &role.ID
	}
	sessionID, err := st.BeginSession(start)
	if errors.Is(err, store.ErrSessionActive) {
		rep.Stop, rep.Detail = StopSessionActive, "a session is already active; end it first"
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	logDispatch := func(msg string) { _, _ = st.LogEvent(nil, &sessionID, stopEventType, msg) }
	logDispatch(fmt.Sprintf("launch planning for spec #%d as architect (budget $%.2f, timeout %s)", spec.ID, opts.StepBudgetUSD, opts.StepTimeout))

	var lastPlan int64
	_ = st.DB.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM plans`).Scan(&lastPlan)
	lastEvent, _ := st.LatestEventID()
	filesBefore := fingerprint(opts.Dir)

	started := time.Now()
	res := agent.Run(ctx, req)
	rep.Launched, rep.Duration, rep.ExitCode, rep.TimedOut, rep.CostUSD = true, time.Since(started), res.ExitCode, res.TimedOut, res.CostUSD
	rep.Summary, rep.Denied = res.Summary, res.Denied
	filesEdited := workspaceChanged(filesBefore, fingerprint(opts.Dir))

	violations, _ := st.CountSessionEventsAfter(sessionID, lastEvent, "policy_violation", "guard_denied")
	summary := fmt.Sprintf("orchestrator planning spec #%d: exit %d in %s", spec.ID, res.ExitCode, rep.Duration.Round(time.Second))
	_ = endSession(st, sessionID, summary, res.CostUSD)

	// The draft the agent submitted, if any: a plan for this spec newer than the run's start.
	var planID int64
	_ = st.DB.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM plans WHERE spec_id = ? AND id > ?`, spec.ID, lastPlan).Scan(&planID)
	rep.PlanID = planID
	logDispatch(fmt.Sprintf("finished planning spec #%d: exit %d, timed_out=%t, cost $%.2f, plan=%d, files_edited=%t, violations=%d, denied=%d%s",
		spec.ID, res.ExitCode, res.TimedOut, res.CostUSD, planID, filesEdited, violations, len(res.Denied), deniedSuffix(res.Denied)))

	switch {
	case ctx.Err() != nil:
		rep.Stop, rep.Detail = StopKilled, "interrupted"
	case res.StartErr != nil:
		rep.Stop, rep.Detail = StopAgentFailed, res.StartErr.Error()
	case filesEdited:
		rep.Stop, rep.Detail = StopViolation, "files changed during a planning run, which is read-only; inspect the working directory"
	case violations > 0:
		rep.Stop, rep.Detail = StopViolation, fmt.Sprintf("%d policy/guard denial(s) recorded during the run", violations)
	case planID == 0 && (res.TimedOut || res.ExitCode != 0):
		rep.Stop, rep.Detail = StopAgentFailed, fmt.Sprintf("agent exited %d (timed out: %t) without proposing a plan", res.ExitCode, res.TimedOut)
	case planID == 0:
		rep.Stop, rep.Detail = StopNoProgress, "the agent finished without proposing a plan"
	}
	if planID == 0 && rep.Stop != StopKilled {
		_, _ = st.AddNote(projectID, fmt.Sprintf("orchestrator planning for spec #%d stopped: %s", spec.ID, rep.Detail), "conversation")
	}
	return rep, nil
}
