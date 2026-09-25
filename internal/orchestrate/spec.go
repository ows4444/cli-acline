package orchestrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"acline/internal/scaffold"
	"acline/internal/store"
	"acline/internal/untrusted"
)

// A spec-drafting run asks one bounded agent to PROPOSE a spec from an idea.
// The agent is read-only except for one command: `acline spec add`. A new spec
// is always a draft (store.AddSpec never creates one as approved), so nothing
// this run produces is derived from — turned into a plan or a task — until a
// person runs `acline spec approve`. Planning has the same shape one step later
// (plan from spec, in plan.go); this is spec from idea.

const (
	maxIdeaLen             = 2000
	maxPromptExistingSpecs = 40
)

// SpecTools returns the allow and deny lists for a spec-drafting run: read
// tools plus the one submit command, and everything that would let the agent
// change files or decide a spec on top of the standing acline denials.
func SpecTools(extra []string) (allow, deny []string) {
	allow = append([]string{"Read", "Grep", "Glob", "Bash(acline spec add *)"}, extra...)
	_, base := Tools(store.RouteVerify, nil)
	deny = append(base,
		"Edit", "Write", "NotebookEdit",
		"Bash(acline spec approve*)", "Bash(acline spec revise*)",
	)
	return allow, deny
}

// SpecPrompt assembles what the drafting agent is told: the idea, the existing
// specs (so it doesn't duplicate one), accepted decisions, approved lessons,
// the designer role contract, the submission format, and the standing rules.
func SpecPrompt(st *store.Store, idea string, projectID *int64) (string, error) {
	var w strings.Builder
	w.WriteString("# Draft a spec from an idea\n\n" + untrusted.Rule + "\n\n")
	w.WriteString("You are turning a short idea into a draft spec. You are read-only: read the codebase to understand it, " +
		"then submit exactly one spec. A person reviews and approves it (`acline spec approve`); nothing derives from it " +
		"until they do. Do not edit any file, and do not try to approve or revise a spec.\n\n")
	fmt.Fprintf(&w, "## The idea\n\n%s\n", truncateText(idea, maxIdeaLen, "(the idea as given; it was not read from a file)"))

	if specs, err := st.ListSpecs("", projectID); err == nil && len(specs) > 0 {
		w.WriteString("\n## Existing specs (do not propose a duplicate; extend or reference one if this idea overlaps)\n\n")
		var b strings.Builder
		for i, sp := range specs {
			if i >= maxPromptExistingSpecs {
				fmt.Fprintf(&b, "- … and %d more (acline spec list)\n", len(specs)-i)
				break
			}
			fmt.Fprintf(&b, "- #%d %s [%s, v%d]\n", sp.ID, sp.Title, sp.Status, sp.Version)
		}
		w.WriteString(untrusted.Quote("Existing specs", b.String()) + "\n")
	}
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
	if c, ok := scaffold.RoleContract("designer"); ok {
		w.WriteString("\n## Role contract: designer\n\n" + demote(c) + "\n")
	}

	w.WriteString(`
## How to write it

- State the problem and the intended behavior, not the implementation. Leave "how" to the architect/developer roles.
- Prefer EARS phrasing for concrete behaviors ("When X, the system shall Y"); acceptance criteria come later, at the task level.
- Say what is explicitly out of scope, if the idea is easy to over-read.
- Keep it to what one plan could reasonably turn into 5-30 tasks. Split an idea that is clearly two specs.
- Ground it in the codebase: name the areas or files it touches. Do not invent parts of the system that are not there.

## Submit

Run exactly this, with your Markdown in place of the example (the heredoc keeps it out of any file):

` + "```" + `
acline spec add "<a short title>" --body-file - <<'SPEC'
...the spec body...
SPEC
` + "```" + `

If it is rejected, read the error, fix it, and run it again. Submit once it succeeds, then stop and summarize the spec in a few lines.
`)
	return w.String(), nil
}

// SpecOptions bound a spec-drafting run. Like Options and PlanOptions, the
// limits are the caller's to set.
type SpecOptions struct {
	Idea          string
	ProjectID     *int64
	StepBudgetUSD float64
	StepTimeout   time.Duration
	Dir           string
	ExtraAllow    []string
	PassEnv       []string // extra environment variable names the agent may inherit
	StopFile      string
	DryRun        bool
	NoSandbox     bool // launch without the Bash sandbox (see sandboxSettings)
}

// SpecReport describes one spec-drafting run.
type SpecReport struct {
	Launched bool
	Stop     Stop
	Detail   string
	SpecID   int64 // the spec the agent proposed, when it did
	ExitCode int
	TimedOut bool
	Duration time.Duration
	CostUSD  float64
	Summary  string
	Denied   []string
	Command  []string // dry run: the agent command line that would run
	Prompt   string   // dry run: the prompt that would be sent
}

// DraftSpec asks an agent to draft a spec from idea. st must carry an agent
// actor. It never approves the spec it drafts.
func DraftSpec(ctx context.Context, st *store.Store, agent Agent, opts SpecOptions) (SpecReport, error) {
	var rep SpecReport
	if st.Actor.Type != "agent" {
		return rep, errors.New("orchestrator must run with an agent actor")
	}
	if strings.TrimSpace(opts.Idea) == "" {
		return rep, errors.New("an idea is required")
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

	prompt, err := SpecPrompt(st, opts.Idea, opts.ProjectID)
	if err != nil {
		return rep, err
	}
	allow, deny := SpecTools(opts.ExtraAllow)
	req := launch{Dir: opts.Dir, PassEnv: opts.PassEnv, NoSandbox: opts.NoSandbox, StepBudgetUSD: opts.StepBudgetUSD, StepTimeout: opts.StepTimeout}.request(st.Path, "designer", prompt, allow, deny)
	if opts.DryRun {
		rep.Stop, rep.Detail, rep.Prompt = StopDryRun, "not launched", prompt
		if c, ok := agent.(ClaudeAgent); ok {
			rep.Command = append([]string{c.bin()}, c.Args(req)...)
		}
		return rep, nil
	}

	start := store.SessionStart{ProjectID: opts.ProjectID, Policy: store.Policy{Label: "orchestrator: draft a spec"}}
	if role, rerr := st.GetRoleByName(opts.ProjectID, "designer"); rerr == nil {
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
	logDispatch(fmt.Sprintf("launch spec drafting as designer (budget $%.2f, timeout %s)", opts.StepBudgetUSD, opts.StepTimeout))

	var lastSpec int64
	_ = st.DB.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM specs`).Scan(&lastSpec)
	lastEvent, _ := st.LatestEventID()
	filesBefore := fingerprint(opts.Dir)

	started := time.Now()
	res := agent.Run(ctx, req)
	rep.Launched, rep.Duration, rep.ExitCode, rep.TimedOut, rep.CostUSD = true, time.Since(started), res.ExitCode, res.TimedOut, res.CostUSD
	rep.Summary, rep.Denied = res.Summary, res.Denied
	filesEdited := workspaceChanged(filesBefore, fingerprint(opts.Dir))

	violations, _ := st.CountSessionEventsAfter(sessionID, lastEvent, "policy_violation", "guard_denied")
	summary := fmt.Sprintf("orchestrator spec drafting: exit %d in %s", res.ExitCode, rep.Duration.Round(time.Second))
	_ = endSession(st, sessionID, summary, res.CostUSD)

	var specID int64
	_ = st.DB.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM specs WHERE id > ?`, lastSpec).Scan(&specID)
	rep.SpecID = specID
	logDispatch(fmt.Sprintf("finished spec drafting: exit %d, timed_out=%t, cost $%.2f, spec=%d, files_edited=%t, violations=%d, denied=%d%s",
		res.ExitCode, res.TimedOut, res.CostUSD, specID, filesEdited, violations, len(res.Denied), deniedSuffix(res.Denied)))

	switch {
	case ctx.Err() != nil:
		rep.Stop, rep.Detail = StopKilled, "interrupted"
	case res.StartErr != nil:
		rep.Stop, rep.Detail = StopAgentFailed, res.StartErr.Error()
	case filesEdited:
		rep.Stop, rep.Detail = StopViolation, "files changed during spec drafting, which is read-only; inspect the working directory"
	case violations > 0:
		rep.Stop, rep.Detail = StopViolation, fmt.Sprintf("%d policy/guard denial(s) recorded during the run", violations)
	case specID == 0 && (res.TimedOut || res.ExitCode != 0):
		rep.Stop, rep.Detail = StopAgentFailed, fmt.Sprintf("agent exited %d (timed out: %t) without proposing a spec", res.ExitCode, res.TimedOut)
	case specID == 0:
		rep.Stop, rep.Detail = StopNoProgress, "the agent finished without proposing a spec"
	}
	if specID == 0 && rep.Stop != StopKilled {
		_, _ = st.AddNote(opts.ProjectID, fmt.Sprintf("orchestrator spec drafting stopped: %s", rep.Detail), "conversation")
	}
	return rep, nil
}
