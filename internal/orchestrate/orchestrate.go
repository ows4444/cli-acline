// Package orchestrate runs a task's next step with an agent, then re-reads the
// recorded state to decide what comes next. It advances work; it never vouches
// for it. docs/ARCHITECTURE.md ("The orchestrator") describes the design; these
// are the invariants this package must hold:
//
//   - It runs as an agent identity and never sees the approval token, so it
//     has no path to approving, reviewing memory, setting runners or forcing
//     the gate.
//   - It only launches steps an agent may take, on tasks that are not
//     human-in-the-loop and not high risk.
//   - The next step is decided from recorded state (RouteTask), never from what
//     the agent says.
//   - Every launch is bounded by a timeout, a spend cap and a step count, and it
//     stops when a person is needed or nothing changed.
//   - It never completes a task: a satisfied gate is reported, not acted on.
package orchestrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"acline/internal/brief"
	"acline/internal/store"
)

// Stop says why the orchestrator did not launch, or did not continue.
type Stop string

const (
	StopNone          Stop = ""
	StopNothing       Stop = "nothing_to_do"
	StopNeedsHuman    Stop = "needs_a_person"
	StopWaiting       Stop = "waiting_on_prerequisite"
	StopComplete      Stop = "ready_to_complete"
	StopSupervised    Stop = "supervised_task"
	StopSessionActive Stop = "session_active"
	StopNoProgress    Stop = "no_progress"
	StopBudget        Stop = "budget_reached"
	StopMaxSteps      Stop = "max_steps"
	StopViolation     Stop = "policy_violation"
	StopReviewPoint   Stop = "review_point"
	StopKilled        Stop = "stopped"
	StopAgentFailed   Stop = "agent_failed"
	StopDryRun        Stop = "dry_run"
)

const (
	stopEventType       = "dispatch"
	defaultActorID      = "orchestrator"
	approvalTokenEnvVar = "ACLINE_APPROVAL_TOKEN"
)

// Options bound and shape a run. Zero values are not safe defaults for the
// limits: callers (the CLI) must set them.
type Options struct {
	TaskID        *int64  // nil: pick with NextTask each step
	ProjectID     *int64  // scope for NextTask
	MaxSteps      int     // launches allowed in Run
	MaxCostUSD    float64 // total spend allowed in Run; 0 = no run-level cap
	StepBudgetUSD float64 // cap handed to each agent run
	StepTimeout   time.Duration
	Dir           string   // agent working directory
	ExtraAllow    []string // added to the per-action allow list
	PassEnv       []string // extra environment variable names the agent may inherit (see agentEnv)
	StopFile      string   // if this file exists, stop before the next launch
	DryRun        bool     // do everything except begin a session and launch
	NoSandbox     bool     // launch without the Bash sandbox (see sandboxSettings); a person's explicit choice
	Log           func(format string, args ...any)
}

// launch is what every kind of step (task step, plan, spec, research) needs to
// build its AgentRequest.
type launch struct {
	Dir           string
	PassEnv       []string
	NoSandbox     bool
	StepBudgetUSD float64
	StepTimeout   time.Duration
}

// request is the launch request for one agent run as role: the environment
// for role, and the sandbox unless the person turned it off.
func (l launch) request(dbPath, role, prompt string, allow, deny []string) AgentRequest {
	req := AgentRequest{
		Prompt: prompt, Dir: l.Dir, Env: agentEnv(os.Environ(), dbPath, role, l.PassEnv),
		AllowTools: allow, DenyTools: deny, MaxBudgetUSD: l.StepBudgetUSD, Timeout: l.StepTimeout,
	}
	if !l.NoSandbox {
		req.Settings = sandboxSettings(dbPath, req.Env)
	}
	return req
}

func (o *Options) logf(format string, args ...any) {
	if o.Log != nil {
		o.Log(format, args...)
	}
}

// StepReport describes one step.
type StepReport struct {
	TaskID      int64
	Action      string // the routed action before launching
	Role        string
	Launched    bool
	Stop        Stop
	Detail      string
	ExitCode    int
	TimedOut    bool
	Duration    time.Duration
	CostUSD     float64
	After       string // the routed action after the step
	Violations  int
	FilesEdited bool     // the agent changed files in the working directory
	Summary     string   // what the agent reported (best effort, for the person supervising)
	Denied      []string // tool calls its permission mode refused (best effort)
	Command     []string // dry run: the agent command line that would run
}

// Report is the outcome of Run.
type Report struct {
	Steps   []StepReport
	Stop    Stop
	Detail  string
	CostUSD float64
}

// agentActions are the steps an agent may take. Everything else is a person's
// (approve_spec, request_approval, resolve_blocker), a finished task, or a
// completion, which stays human in v1.
var agentActions = map[string]bool{
	store.RouteDefineCriteria: true,
	store.RouteStartWork:      true,
	store.RouteFixChecks:      true,
	store.RouteVerify:         true,
}

// Eligible decides whether a routed step may be launched. It returns StopNone
// when it may, otherwise why not. It is deliberately pure: the launch rules are
// the safety surface, so they are tested directly.
func Eligible(t *store.Task, r *store.Route) (Stop, string) {
	switch {
	case r.Action == store.RouteNone:
		return StopNothing, r.Reason
	case r.Action == store.RouteWaitDependency:
		return StopWaiting, fmt.Sprintf("%s: %s", r.Reason, strings.Join(r.WaitingOn, "; "))
	case r.NeedsHuman:
		return StopNeedsHuman, fmt.Sprintf("%s: %s", r.Action, r.Reason)
	case r.Action == store.RouteComplete:
		return StopComplete, fmt.Sprintf("the gate is satisfied; complete it yourself: acline task done %d", t.ID)
	case !agentActions[r.Action]:
		return StopNothing, "no agent step for " + r.Action
	case t.Autonomy == "hitl":
		return StopSupervised, "autonomy is hitl: use `acline brief` and run the step yourself"
	case t.Risk == "high" || t.Risk == "critical":
		return StopSupervised, fmt.Sprintf("risk is %s: use `acline brief` and run the step yourself", t.Risk)
	}
	return StopNone, ""
}

// Tools returns the allow and deny lists for an action. Deny always wins and
// names the acline commands an agent must never run; the allow list is what
// the step needs and nothing else. Both use Claude Code's permission syntax.
func Tools(action string, extra []string) (allow, deny []string) {
	read := []string{"Read", "Grep", "Glob"}
	acline := []string{"Bash(acline check run *)", "Bash(acline check list *)", "Bash(acline task show *)", "Bash(acline brief *)"}
	build := []string{"Bash(go build *)", "Bash(go test *)", "Bash(go vet *)", "Bash(git status*)", "Bash(git diff*)"}
	switch action {
	case store.RouteDefineCriteria:
		allow = append(read, "Bash(acline task criteria *)", "Bash(acline task show *)")
	case store.RouteVerify:
		allow = append(append(read, acline...), build...)
	default: // start_work, fix_failing_checks
		allow = append(append(append(read, "Edit", "Write"), acline...), build...)
	}
	allow = append(allow, extra...)
	deny = []string{
		"Bash(acline approve*)", "Bash(acline reject*)", "Bash(acline auth*)", "Bash(acline memory *)",
		"Bash(acline check runner*)", "Bash(acline orchestrate*)", "Bash(acline snapshot*)", "Bash(acline task done*)",
		"Bash(acline project add*)", "Bash(acline init*)", "Bash(acline check record*)", "Bash(acline session*)", "Bash(acline task update*)",
		"Bash(git push*)", "Bash(git commit*)", "Bash(rm *)", "WebFetch",
	}
	return allow, deny
}

// RunnerTools allows the commands a person configured as the project's check
// runners (`acline check runner set`), so a step can build and test a project
// that isn't Go the way it would be verified. Only a person sets runners, so
// this widens the allow list only by what a person already chose to execute.
// A command that can't be written as a permission pattern is left out.
func RunnerTools(runners []store.CheckRunner) []string {
	var out []string
	for _, r := range runners {
		cmd := strings.Join(strings.Fields(r.Command), " ")
		if cmd == "" || strings.ContainsAny(cmd, "()*") {
			continue
		}
		out = append(out, "Bash("+cmd+")", "Bash("+cmd+" *)")
	}
	return out
}

// agentEnvAllow and agentEnvPrefixes are the only variables the agent inherits.
// It runs shell commands (`go test` executes arbitrary code), so a deny-list of
// known secrets is the wrong shape: cloud credentials, GH_TOKEN, NPM_TOKEN,
// database URLs and SSH agent sockets would all pass through it. What is here is
// what a build/test step and Claude Code itself need. Anything else the person
// launching wants the agent to have is named explicitly with --pass-env.
var agentEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true, "TERM": true, "TZ": true, "LANG": true,
	"TMPDIR": true, "TEMP": true, "TMP": true, "PWD": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "ALL_PROXY": true,
	"http_proxy": true, "https_proxy": true, "no_proxy": true, "all_proxy": true,
	"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true, "REQUESTS_CA_BUNDLE": true,
	// the Go toolchain, so `go build|test|vet` behave as they do for the person
	"GOPATH": true, "GOCACHE": true, "GOMODCACHE": true, "GOFLAGS": true, "GOROOT": true, "GOPROXY": true,
	"GONOSUMDB": true, "GONOSUMCHECK": true, "GOPRIVATE": true, "GOTOOLCHAIN": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true,
	// Windows
	"SYSTEMROOT": true, "USERPROFILE": true, "APPDATA": true, "LOCALAPPDATA": true, "COMSPEC": true, "PATHEXT": true,
}

var agentEnvPrefixes = []string{"ANTHROPIC_", "CLAUDE_", "LC_", "XDG_"}

// agentEnvForced are set by the orchestrator and can never be inherited or
// requested through passEnv: an agent that could act as a human could approve
// its own work.
var agentEnvForced = map[string]bool{
	approvalTokenEnvVar: true, "ACLINE_ACTOR_TYPE": true, "ACLINE_ACTOR": true, "ACLINE_ROLE": true, "ACLINE_DB": true,
}

func agentEnvAllowed(key string, extra map[string]bool) bool {
	if agentEnvForced[key] {
		return false
	}
	if agentEnvAllow[key] || extra[key] {
		return true
	}
	for _, p := range agentEnvPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// agentEnv is the environment the agent runs with: an allow-listed subset of the
// caller's (plus the variables named in passEnv), never the approval token, with
// agent identity forced. An agent that could act as a human could approve its own
// work, so identity is set here, not left to inherit.
func agentEnv(environ []string, dbPath, role string, passEnv []string) []string {
	extra := map[string]bool{}
	for _, name := range passEnv {
		extra[strings.TrimSpace(name)] = true
	}
	var env []string
	for _, kv := range environ {
		if k, _, _ := strings.Cut(kv, "="); agentEnvAllowed(k, extra) {
			env = append(env, kv)
		}
	}
	env = append(env, "ACLINE_ACTOR_TYPE=agent", "ACLINE_ACTOR="+defaultActorID)
	if dbPath != "" {
		env = append(env, "ACLINE_DB="+dbPath)
	}
	if role != "" {
		env = append(env, "ACLINE_ROLE="+role)
	}
	return env
}

// PreflightHooks refuses to launch an agent in a project whose Claude Code
// settings do not run the guard on every tool it checks. Without the PreToolUse
// hook the agent's Read/Edit/Write/Bash calls are not screened for secret files,
// protected files or write scope, and the allow list alone is the only limit.
// required is the set of tool names `acline guard check-tool` inspects; the
// hook's matcher must cover all of them. Coverage may be split across
// .claude/settings.json and .claude/settings.local.json.
func PreflightHooks(dir string, required []string) error {
	covered := map[string]bool{}
	found := false
	var hookSeen bool
	for _, name := range []string{"settings.json", "settings.local.json"} {
		path := filepath.Join(dir, ".claude", name)
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		found = true
		var cfg struct {
			Hooks struct {
				PreToolUse []struct {
					Matcher string `json:"matcher"`
					Hooks   []struct {
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"PreToolUse"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		for _, entry := range cfg.Hooks.PreToolUse {
			hookSeen = true
			runsGuard := false
			for _, h := range entry.Hooks {
				if strings.Contains(h.Command, "hook pre-tool-use") || strings.Contains(h.Command, "guard check-tool") || strings.Contains(h.Command, "pre_tool_use") {
					runsGuard = true
				}
			}
			if !runsGuard {
				continue
			}
			for _, tool := range strings.Split(entry.Matcher, "|") {
				covered[strings.TrimSpace(tool)] = true
			}
		}
	}
	switch {
	case !found:
		return fmt.Errorf("%s has no .claude/settings.json, so the guard hook is not installed and this agent's tool calls would not be screened; run `acline init` here, or pass --allow-unprotected to accept that risk", dir)
	case !hookSeen:
		return fmt.Errorf("%s has no PreToolUse hook, so the guard is not running; run `acline init --upgrade` here, or pass --allow-unprotected to accept that risk", dir)
	}
	var missing []string
	for _, tool := range required {
		if !covered[tool] {
			missing = append(missing, tool)
		}
	}
	if len(covered) == 0 {
		return fmt.Errorf("%s has a PreToolUse hook, but none of them runs the acline guard (`acline guard check-tool`); run `acline init --upgrade`, or pass --allow-unprotected to accept that risk", dir)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%s: the guard's PreToolUse matcher does not cover %s, so those tool calls bypass it; run `acline guard doctor` and `acline init --upgrade`, or pass --allow-unprotected to accept that risk", dir, strings.Join(missing, ", "))
	}
	return nil
}

// signature captures the parts of a task's recorded state a step is supposed to
// change. If it is identical before and after, the step made no progress.
func signature(r *store.Route) string {
	checks := append([]string(nil), r.FailingChecks...)
	sort.Strings(checks)
	return fmt.Sprintf("%s|%s|%s|blockers=%d|open=%d", r.Action, r.Status, strings.Join(checks, ";"), len(r.Blockers), len(r.OpenCriteria))
}

// Preflight refuses to launch agents when an agent could trivially act as a
// person. Without an approval token, ACLINE_ACTOR_TYPE=human is all it takes to
// self-approve, so autonomous launching requires the token to be enabled.
func Preflight(st *store.Store, allowUnprotected bool) error {
	if allowUnprotected {
		return nil
	}
	on, err := st.ApprovalTokenEnabled()
	if err != nil {
		return err
	}
	if !on {
		return errors.New("no approval token is enabled, so an agent could act as a human and approve its own work; " +
			"run `acline auth init`, or pass --allow-unprotected to accept that risk")
	}
	return nil
}

// pick chooses the highest-priority task the orchestrator may launch. When none
// is launchable it returns the first open task's route so the caller can say why
// (a person is needed, the task is supervised, ...). nil means no open tasks.
func pick(st *store.Store, projectID *int64) (*store.Route, error) {
	tasks, err := st.ListTasks(store.TaskFilter{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	var first *store.Route
	for i := range tasks {
		r, err := st.RouteTask(tasks[i].ID)
		if err != nil {
			return nil, err
		}
		if r.Action == store.RouteNone {
			continue
		}
		if stop, _ := Eligible(&tasks[i], r); stop == StopNone {
			return r, nil
		}
		if first == nil {
			first = r
		}
	}
	return first, nil
}

// Step routes one task and, when the rules allow, runs it with agent. st must
// carry an agent actor: the orchestrator is never a human.
func Step(ctx context.Context, st *store.Store, agent Agent, opts Options) (StepReport, error) {
	if st.Actor.Type != "agent" {
		return StepReport{}, errors.New("orchestrator must run with an agent actor")
	}
	var route *store.Route
	var err error
	if opts.TaskID != nil {
		route, err = st.RouteTask(*opts.TaskID)
	} else {
		route, err = pick(st, opts.ProjectID)
	}
	if err != nil {
		return StepReport{}, err
	}
	if route == nil {
		return StepReport{Stop: StopNothing, Detail: "no open tasks"}, nil
	}
	rep := StepReport{TaskID: route.TaskID, Action: route.Action, After: route.Action}
	if route.Role != nil {
		rep.Role = route.Role.Name
	}
	task, err := st.GetTask(route.TaskID)
	if err != nil {
		return rep, err
	}
	if stop, why := Eligible(task, route); stop != StopNone {
		rep.Stop, rep.Detail = stop, why
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

	b, err := brief.Build(st, route.TaskID)
	if err != nil {
		return rep, err
	}
	extra := opts.ExtraAllow
	if route.Action != store.RouteDefineCriteria && task.ProjectID.Valid {
		runners, err := st.ListCheckRunners(task.ProjectID.Int64)
		if err != nil {
			return rep, err
		}
		extra = append(RunnerTools(runners), extra...)
	}
	allow, deny := Tools(route.Action, extra)
	req := launch{Dir: opts.Dir, PassEnv: opts.PassEnv, NoSandbox: opts.NoSandbox, StepBudgetUSD: opts.StepBudgetUSD, StepTimeout: opts.StepTimeout}.request(st.Path, rep.Role, b.Markdown(), allow, deny)
	if opts.DryRun {
		rep.Stop, rep.Detail = StopDryRun, "not launched"
		if c, ok := agent.(ClaudeAgent); ok {
			rep.Command = append([]string{c.bin()}, c.Args(req)...)
		}
		return rep, nil
	}

	// define_criteria writes criteria, not code: link the session to no task so
	// starting it does not move the task to in_progress and change its route.
	start := store.SessionStart{ProjectID: nil, Policy: store.Policy{Label: fmt.Sprintf("orchestrator: %s task #%d", route.Action, route.TaskID)}}
	if route.Action != store.RouteDefineCriteria {
		start.TaskID = &route.TaskID
	}
	if task.ProjectID.Valid {
		start.ProjectID = &task.ProjectID.Int64
	}
	if route.Role != nil {
		start.RoleID = &route.Role.ID
	}
	sessionID, err := st.BeginSession(start)
	if errors.Is(err, store.ErrSessionActive) {
		rep.Stop, rep.Detail = StopSessionActive, "a session is already active; end it first"
		return rep, nil
	}
	if err != nil {
		return rep, err
	}
	taskID := route.TaskID
	logDispatch := func(msg string) {
		_, _ = st.LogEvent(&taskID, &sessionID, stopEventType, msg)
	}
	logDispatch(fmt.Sprintf("launch %s as %s (budget $%.2f, timeout %s)", route.Action, orDash(rep.Role), opts.StepBudgetUSD, opts.StepTimeout))

	// Progress is measured from here, after the session start (which moves a
	// linked task to in_progress), so that move is not mistaken for the agent's work.
	baseline, err := st.RouteTask(taskID)
	if err != nil {
		_ = endSession(st, sessionID, "orchestrator: could not snapshot state", 0)
		return rep, err
	}
	lastEvent, _ := st.LatestEventID()
	filesBefore := fingerprint(opts.Dir)

	started := time.Now()
	res := agent.Run(ctx, req)
	rep.Summary, rep.Denied = res.Summary, res.Denied
	rep.FilesEdited = workspaceChanged(filesBefore, fingerprint(opts.Dir))
	rep.Launched, rep.Duration, rep.ExitCode, rep.TimedOut, rep.CostUSD = true, time.Since(started), res.ExitCode, res.TimedOut, res.CostUSD

	rep.Violations, _ = st.CountSessionEventsAfter(sessionID, lastEvent, "policy_violation", "guard_denied")
	summary := fmt.Sprintf("orchestrator step %s: exit %d in %s", route.Action, res.ExitCode, rep.Duration.Round(time.Second))
	if res.TimedOut {
		summary += " (timed out)"
	}
	_ = endSession(st, sessionID, summary, res.CostUSD)
	logDispatch(fmt.Sprintf("finished %s: exit %d, timed_out=%t, cost $%.2f, violations=%d, files_edited=%t, turns=%d, denied=%d%s",
		route.Action, res.ExitCode, res.TimedOut, res.CostUSD, rep.Violations, rep.FilesEdited, res.Turns, len(res.Denied), deniedSuffix(res.Denied)))

	after, err := st.RouteTask(taskID)
	if err != nil {
		return rep, err
	}
	rep.After = after.Action

	switch {
	case ctx.Err() != nil:
		rep.Stop, rep.Detail = StopKilled, "interrupted"
	case res.StartErr != nil:
		rep.Stop, rep.Detail = StopAgentFailed, res.StartErr.Error()
	case rep.Violations > 0:
		rep.Stop, rep.Detail = StopViolation, fmt.Sprintf("%d policy/guard denial(s) recorded during the step", rep.Violations)
	case res.TimedOut || res.ExitCode != 0:
		rep.Stop, rep.Detail = StopAgentFailed, fmt.Sprintf("agent exited %d (timed out: %t)", res.ExitCode, res.TimedOut)
	case signature(after) == signature(baseline) && !rep.FilesEdited:
		rep.Stop, rep.Detail = StopNoProgress, "neither the recorded state nor any file changed"
	}
	if rep.Stop == StopAgentFailed || rep.Stop == StopNoProgress || rep.Stop == StopViolation {
		// A note, not a memory: it waits for `reflect` and a person, and is not trusted context.
		note := fmt.Sprintf("orchestrator stopped on task #%d (%s): %s", taskID, route.Action, rep.Detail)
		if _, err := st.AddNote(nullable(task.ProjectID.Valid, task.ProjectID.Int64), note, "conversation"); err != nil {
			logDispatch("could not record a note about the stop: " + err.Error())
		}
	}
	return rep, nil
}

func deniedSuffix(denied []string) string {
	if len(denied) == 0 {
		return ""
	}
	return " (" + strings.Join(denied, "; ") + ")"
}

func endSession(st *store.Store, id int64, summary string, cost float64) error {
	if cur, err := st.CurrentSession(); err != nil || cur.ID != id {
		return nil // the agent already ended it, or it is not ours
	}
	// The orchestrator launched this session, so it may close it even when the
	// step ran as a read-only role (see Store.EndLaunchedSession).
	_, err := st.EndLaunchedSession(id, summary, store.SessionCost{CostUSD: cost})
	return err
}

// Run repeats Step until a stop condition. Only autonomy=auto tasks chain: a
// hotl task gets one step and then a review point.
func Run(ctx context.Context, st *store.Store, agent Agent, opts Options) (Report, error) {
	var rep Report
	for i := 0; ; i++ {
		if opts.MaxSteps > 0 && i >= opts.MaxSteps {
			rep.Stop, rep.Detail = StopMaxSteps, fmt.Sprintf("reached --max-steps %d", opts.MaxSteps)
			return rep, nil
		}
		step := opts
		if opts.MaxCostUSD > 0 {
			left := opts.MaxCostUSD - rep.CostUSD
			if left <= 0 {
				rep.Stop, rep.Detail = StopBudget, fmt.Sprintf("spent $%.2f of $%.2f", rep.CostUSD, opts.MaxCostUSD)
				return rep, nil
			}
			if step.StepBudgetUSD <= 0 || left < step.StepBudgetUSD {
				step.StepBudgetUSD = left
			}
		}
		sr, err := Step(ctx, st, agent, step)
		if err != nil {
			return rep, err
		}
		rep.Steps = append(rep.Steps, sr)
		rep.CostUSD += sr.CostUSD
		opts.logf("step %d: task #%d %s -> %s (%s)", i+1, sr.TaskID, sr.Action, sr.After, stopOr(sr))
		if sr.Stop != StopNone {
			rep.Stop, rep.Detail = sr.Stop, sr.Detail
			return rep, nil
		}
		if t, err := st.GetTask(sr.TaskID); err == nil && t.Autonomy != "auto" {
			rep.Stop, rep.Detail = StopReviewPoint, fmt.Sprintf("task #%d is %s: one step, then a person reviews", t.ID, t.Autonomy)
			return rep, nil
		}
	}
}

func stopOr(sr StepReport) string {
	if sr.Stop != StopNone {
		return string(sr.Stop)
	}
	return "progressed"
}

func orDash(s string) string {
	if s == "" {
		return "any role"
	}
	return s
}

func nullable(ok bool, v int64) *int64 {
	if !ok {
		return nil
	}
	return &v
}
