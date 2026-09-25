package orchestrate

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"acline/internal/store"
)

// fakeAgent stands in for the agent: do() performs whatever "work" the test
// wants recorded, and the request and call count are kept for assertions.
type fakeAgent struct {
	calls int
	reqs  []AgentRequest
	do    func(req AgentRequest)
	res   AgentResult
}

func (f *fakeAgent) Run(ctx context.Context, req AgentRequest) AgentResult {
	f.calls++
	f.reqs = append(f.reqs, req)
	if f.do != nil {
		f.do(req)
	}
	return f.res
}

func newAgentStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "o.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.Actor = store.Actor{Type: "agent", ID: "orchestrator"}
	t.Cleanup(func() { s.Close() })
	return s
}

func opts() Options {
	return Options{StepBudgetUSD: 1, StepTimeout: time.Minute, MaxSteps: 5}
}

// asPerson runs fn with s acting as a human: only a person may create a task at
// autonomy "auto", so test setup that needs one plays that part.
func asPerson(s *store.Store, fn func()) {
	agent := s.Actor
	s.Actor = store.Actor{Type: "human", ID: "setup"}
	defer func() { s.Actor = agent }()
	fn()
}

// readyTask makes a task that routes to start_work.
func readyTask(t *testing.T, s *store.Store, o store.TaskOpts) int64 {
	t.Helper()
	var id int64
	var err error
	asPerson(s, func() { id, err = s.AddTask("do it", "", "normal", o) })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddCriterion(id, "When X, the system shall Y"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEligibleRules(t *testing.T) {
	cases := []struct {
		name  string
		task  store.Task
		route store.Route
		want  Stop
	}{
		{"agent step", store.Task{Risk: "low", Autonomy: "hotl"}, store.Route{Action: store.RouteStartWork}, StopNone},
		{"verify", store.Task{Risk: "medium", Autonomy: "auto"}, store.Route{Action: store.RouteVerify}, StopNone},
		{"nothing", store.Task{}, store.Route{Action: store.RouteNone}, StopNothing},
		{"needs a person", store.Task{Risk: "low"}, store.Route{Action: store.RouteApproveSpec, NeedsHuman: true}, StopNeedsHuman},
		{"complete stays human", store.Task{Risk: "low"}, store.Route{Action: store.RouteComplete}, StopComplete},
		{"hitl", store.Task{Risk: "low", Autonomy: "hitl"}, store.Route{Action: store.RouteStartWork}, StopSupervised},
		{"high risk", store.Task{Risk: "high", Autonomy: "auto"}, store.Route{Action: store.RouteStartWork}, StopSupervised},
		{"critical risk", store.Task{Risk: "critical", Autonomy: "auto"}, store.Route{Action: store.RouteFixChecks}, StopSupervised},
	}
	for _, c := range cases {
		if got, _ := Eligible(&c.task, &c.route); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestToolsNeverAllowApprovalOrReviewAndDenyWins(t *testing.T) {
	for _, action := range []string{store.RouteDefineCriteria, store.RouteStartWork, store.RouteFixChecks, store.RouteVerify} {
		allow, deny := Tools(action, nil)
		joined := strings.Join(allow, ",")
		for _, bad := range []string{"acline approve", "acline auth", "acline memory", "acline check runner", "git push", "WebFetch"} {
			if strings.Contains(joined, bad) {
				t.Errorf("%s allows %q", action, bad)
			}
		}
		for _, must := range []string{"Bash(acline approve*)", "Bash(acline auth*)", "Bash(acline memory *)", "Bash(acline check runner*)", "Bash(acline task done*)", "Bash(git push*)"} {
			if !contains(deny, must) {
				t.Errorf("%s does not deny %q", action, must)
			}
		}
	}
	if allow, _ := Tools(store.RouteVerify, nil); contains(allow, "Edit") || contains(allow, "Write") {
		t.Error("verify must not be able to edit")
	}
	if allow, _ := Tools(store.RouteStartWork, []string{"Bash(make *)"}); !contains(allow, "Bash(make *)") || !contains(allow, "Edit") {
		t.Errorf("start_work allow = %v", allow)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func TestAgentEnvForcesAgentIdentityAndDropsTheToken(t *testing.T) {
	env := agentEnv([]string{"PATH=/bin", "ACLINE_APPROVAL_TOKEN=secret", "ACLINE_ACTOR_TYPE=human", "ACLINE_ACTOR=me", "ACLINE_DB=/other", "HOME=/h"}, "/db/path", "developer", nil)
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"secret", "ACLINE_ACTOR_TYPE=human", "ACLINE_ACTOR=me", "/other"} {
		if strings.Contains(joined, bad) {
			t.Errorf("env still contains %q:\n%s", bad, joined)
		}
	}
	for _, want := range []string{"PATH=/bin", "HOME=/h", "ACLINE_ACTOR_TYPE=agent", "ACLINE_ACTOR=orchestrator", "ACLINE_DB=/db/path", "ACLINE_ROLE=developer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q", want)
		}
	}
}

func TestStepRefusesToRunAsAHuman(t *testing.T) {
	s := newAgentStore(t)
	s.Actor = store.Actor{Type: "human", ID: "me"}
	if _, err := Step(context.Background(), s, &fakeAgent{}, opts()); err == nil {
		t.Fatal("a human actor was accepted")
	}
}

func TestStepLaunchesTheRoutedStepAndRecordsIt(t *testing.T) {
	t.Setenv("ACLINE_APPROVAL_TOKEN", "must-not-leak")
	s := newAgentStore(t)
	id := readyTask(t, s, store.TaskOpts{Area: "store"})
	f := &fakeAgent{res: AgentResult{CostUSD: 0.25}, do: func(AgentRequest) { s.AddCheck(id, "test", "pass", "ok") }}

	rep, err := Step(context.Background(), s, f, opts())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Launched || rep.Stop != StopNone || rep.Action != store.RouteStartWork || rep.After != store.RouteComplete || rep.CostUSD != 0.25 {
		t.Fatalf("report = %+v", rep)
	}
	req := f.reqs[0]
	if !strings.Contains(req.Prompt, "# Task #1: do it") || !strings.Contains(req.Prompt, "start_work") {
		t.Errorf("prompt is not the brief:\n%s", req.Prompt)
	}
	if strings.Contains(strings.Join(req.Env, "\n"), "must-not-leak") || !contains(req.Env, "ACLINE_ACTOR_TYPE=agent") {
		t.Errorf("agent env is not scrubbed: %v", req.Env)
	}
	if req.MaxBudgetUSD != 1 || !contains(req.AllowTools, "Edit") || !contains(req.DenyTools, "Bash(acline approve*)") {
		t.Errorf("request limits = %+v", req)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("the step left its session open")
	}
	evs, _ := s.ListEvents(&id, 20)
	var launches, finishes int
	for _, e := range evs {
		if e.Type == "dispatch" {
			launches += strings.Count(e.Message, "launch ")
			finishes += strings.Count(e.Message, "finished ")
		}
	}
	if launches != 1 || finishes != 1 {
		t.Errorf("dispatch events launch=%d finish=%d: %+v", launches, finishes, evs)
	}
}

func TestStepStopsWhenNothingChanged(t *testing.T) {
	s := newAgentStore(t)
	id := readyTask(t, s, store.TaskOpts{})
	rep, _ := Step(context.Background(), s, &fakeAgent{}, opts())
	if rep.Stop != StopNoProgress || !rep.Launched {
		t.Fatalf("report = %+v", rep)
	}
	// A step that only flipped the task to in_progress is not progress.
	if got, _ := s.GetTask(id); got.Status != "in_progress" {
		t.Fatalf("status = %s", got.Status)
	}
	notes, _ := s.ListNotes(store.NoteFilter{UnpromotedOnly: true})
	if len(notes) != 1 || !strings.Contains(notes[0].Body, "orchestrator stopped on task #1") {
		t.Errorf("notes = %+v", notes)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("session left open")
	}
}

func TestStepStopsOnAgentFailureTimeoutAndViolation(t *testing.T) {
	for _, c := range []struct {
		name string
		f    func(s *store.Store, id int64) *fakeAgent
		want Stop
	}{
		{"nonzero exit", func(s *store.Store, id int64) *fakeAgent {
			return &fakeAgent{res: AgentResult{ExitCode: 2}, do: func(AgentRequest) { s.AddCheck(id, "test", "pass", "") }}
		}, StopAgentFailed},
		{"timeout", func(s *store.Store, id int64) *fakeAgent {
			return &fakeAgent{res: AgentResult{ExitCode: -1, TimedOut: true}}
		}, StopAgentFailed},
		{"cannot start", func(s *store.Store, id int64) *fakeAgent {
			return &fakeAgent{res: AgentResult{ExitCode: -1, StartErr: context.DeadlineExceeded}}
		}, StopAgentFailed},
		{"policy violation even if progress was made", func(s *store.Store, id int64) *fakeAgent {
			return &fakeAgent{do: func(AgentRequest) {
				s.AddCheck(id, "test", "pass", "")
				s.LogEventGlobal("policy_violation", "denied Bash")
			}}
		}, StopViolation},
	} {
		s := newAgentStore(t)
		id := readyTask(t, s, store.TaskOpts{})
		rep, _ := Step(context.Background(), s, c.f(s, id), opts())
		if rep.Stop != c.want {
			t.Errorf("%s: stop = %q (%s), want %q", c.name, rep.Stop, rep.Detail, c.want)
		}
	}
}

func TestStepDoesNotLaunchWhenItShouldNot(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(s *store.Store) (Options, int64)
		want Stop
	}{
		{"supervised (hitl)", func(s *store.Store) (Options, int64) {
			return opts(), readyTask(t, s, store.TaskOpts{Autonomy: "hitl"})
		}, StopSupervised},
		{"supervised (high risk)", func(s *store.Store) (Options, int64) {
			return opts(), readyTask(t, s, store.TaskOpts{Risk: "high", Autonomy: "auto"})
		}, StopSupervised},
		{"needs a person", func(s *store.Store) (Options, int64) {
			id, _ := s.AddTask("t", "", "normal", store.TaskOpts{})
			s.SetTaskStatus(id, "blocked")
			return opts(), id
		}, StopNeedsHuman},
		{"ready to complete", func(s *store.Store) (Options, int64) {
			id := readyTask(t, s, store.TaskOpts{})
			s.SetTaskStatus(id, "in_progress")
			s.AddCheck(id, "test", "pass", "")
			return opts(), id
		}, StopComplete},
		{"session already active", func(s *store.Store) (Options, int64) {
			s.BeginSession(store.SessionStart{})
			return opts(), readyTask(t, s, store.TaskOpts{})
		}, StopSessionActive},
		{"stop file", func(s *store.Store) (Options, int64) {
			o := opts()
			o.StopFile = filepath.Join(t.TempDir(), "stop")
			writeFile(t, o.StopFile)
			return o, readyTask(t, s, store.TaskOpts{})
		}, StopKilled},
	} {
		s := newAgentStore(t)
		o, id := c.set(s)
		o.TaskID = &id
		f := &fakeAgent{}
		rep, err := Step(context.Background(), s, f, o)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rep.Stop != c.want || rep.Launched || f.calls != 0 {
			t.Errorf("%s: stop=%q launched=%t calls=%d (%s)", c.name, rep.Stop, rep.Launched, f.calls, rep.Detail)
		}
	}
}

func TestDefineCriteriaDoesNotMoveTheTask(t *testing.T) {
	s := newAgentStore(t)
	id, _ := s.AddTask("t", "", "normal", store.TaskOpts{}) // no criteria: define_criteria
	f := &fakeAgent{do: func(AgentRequest) { s.AddCriterion(id, "When X, the system shall Y") }}
	rep, _ := Step(context.Background(), s, f, opts())
	if rep.Action != store.RouteDefineCriteria || rep.Stop != StopNone || rep.After != store.RouteStartWork {
		t.Fatalf("report = %+v", rep)
	}
	if got, _ := s.GetTask(id); got.Status != "todo" {
		t.Errorf("status = %s, want todo", got.Status)
	}
	if allow, _ := Tools(store.RouteDefineCriteria, nil); contains(allow, "Edit") {
		t.Error("define_criteria must not edit code")
	}
}

func TestDryRunLaunchesNothingAndShowsTheCommand(t *testing.T) {
	s := newAgentStore(t)
	readyTask(t, s, store.TaskOpts{})
	o := opts()
	o.DryRun = true
	rep, err := Step(context.Background(), s, ClaudeAgent{}, o)
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(rep.Command, " ")
	if rep.Stop != StopDryRun || rep.Launched || !strings.Contains(cmd, "--permission-mode dontAsk") {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("a dry run began a session")
	}
	if got, _ := s.GetTask(1); got.Status != "todo" {
		t.Errorf("a dry run changed the task: %s", got.Status)
	}
}

func TestRunChainsOnlyAutoTasksAndHonoursItsBounds(t *testing.T) {
	// Each step "does work": it adds a failing check for the next step to fix,
	// so state always changes.
	progress := func(s *store.Store) func(AgentRequest) {
		n := 0
		return func(AgentRequest) {
			n++
			s.AddCheck(1, "test", "fail", strings.Repeat("x", n))
		}
	}

	t.Run("hotl stops for review after one step", func(t *testing.T) {
		s := newAgentStore(t)
		readyTask(t, s, store.TaskOpts{Autonomy: "hotl"})
		f := &fakeAgent{do: progress(s)}
		rep, _ := Run(context.Background(), s, f, opts())
		if rep.Stop != StopReviewPoint || f.calls != 1 {
			t.Fatalf("stop=%q calls=%d", rep.Stop, f.calls)
		}
	})
	t.Run("auto stops at max steps", func(t *testing.T) {
		s := newAgentStore(t)
		readyTask(t, s, store.TaskOpts{Autonomy: "auto"})
		f := &fakeAgent{do: progress(s)}
		o := opts()
		o.MaxSteps = 2
		rep, _ := Run(context.Background(), s, f, o)
		if rep.Stop != StopMaxSteps || f.calls != 2 || len(rep.Steps) != 2 {
			t.Fatalf("stop=%q calls=%d", rep.Stop, f.calls)
		}
	})
	t.Run("auto stops when the budget is spent and caps the next step", func(t *testing.T) {
		s := newAgentStore(t)
		readyTask(t, s, store.TaskOpts{Autonomy: "auto"})
		f := &fakeAgent{res: AgentResult{CostUSD: 0.6}, do: progress(s)}
		o := opts()
		o.MaxCostUSD = 1.0
		rep, _ := Run(context.Background(), s, f, o)
		if rep.Stop != StopBudget || f.calls != 2 {
			t.Fatalf("stop=%q calls=%d cost=%.2f", rep.Stop, f.calls, rep.CostUSD)
		}
		if f.reqs[0].MaxBudgetUSD != 1 || f.reqs[1].MaxBudgetUSD < 0.39 || f.reqs[1].MaxBudgetUSD > 0.41 {
			t.Errorf("per-step caps = %v, %v (want 1 then the 0.40 left)", f.reqs[0].MaxBudgetUSD, f.reqs[1].MaxBudgetUSD)
		}
	})
	t.Run("auto stops on no progress", func(t *testing.T) {
		s := newAgentStore(t)
		readyTask(t, s, store.TaskOpts{Autonomy: "auto"})
		f := &fakeAgent{}
		rep, _ := Run(context.Background(), s, f, opts())
		if rep.Stop != StopNoProgress || f.calls != 1 {
			t.Fatalf("stop=%q calls=%d", rep.Stop, f.calls)
		}
	})
	t.Run("skips a supervised task for a launchable one", func(t *testing.T) {
		s := newAgentStore(t)
		var low int64
		asPerson(s, func() {
			s.AddTask("urgent but risky", "", "urgent", store.TaskOpts{Risk: "high", Autonomy: "auto"})
			low, _ = s.AddTask("ordinary", "", "low", store.TaskOpts{Autonomy: "auto"})
		})
		s.AddCriterion(low, "When X, the system shall Y")
		f := &fakeAgent{do: func(AgentRequest) { s.AddCheck(low, "test", "pass", "") }}
		rep, _ := Run(context.Background(), s, f, opts())
		if f.calls != 1 || rep.Steps[0].TaskID != low {
			t.Fatalf("calls=%d steps=%+v", f.calls, rep.Steps)
		}
	})
}

func TestPreflightRequiresTheApprovalToken(t *testing.T) {
	s := newAgentStore(t)
	if err := Preflight(s, false); err == nil || !strings.Contains(err.Error(), "approval token") {
		t.Fatalf("no token: %v", err)
	}
	if err := Preflight(s, true); err != nil {
		t.Errorf("explicit override refused: %v", err)
	}
	s.Actor = store.Actor{Type: "human", ID: "me"}
	if _, err := s.EnableApprovalToken(); err != nil {
		t.Fatal(err)
	}
	if err := Preflight(s, false); err != nil {
		t.Errorf("token enabled but refused: %v", err)
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := osWriteFile(path); err != nil {
		t.Fatal(err)
	}
}

func TestOrchestratorSkipsATaskWaitingOnAPrerequisite(t *testing.T) {
	s := newAgentStore(t)
	dep := readyTask(t, s, store.TaskOpts{})
	waiting, _ := s.AddTask("urgent, waiting", "", "urgent", store.TaskOpts{})
	s.AddCriterion(waiting, "When X, the system shall Y")
	if _, err := s.AddLink(waiting, dep, "depends_on"); err != nil {
		t.Fatal(err)
	}
	// Named directly: refused, with the reason.
	f := &fakeAgent{}
	o := opts()
	o.TaskID = &waiting
	rep, _ := Step(context.Background(), s, f, o)
	if rep.Stop != StopWaiting || rep.Launched || f.calls != 0 || !strings.Contains(rep.Detail, "#1") {
		t.Fatalf("named: %+v", rep)
	}
	// Picked automatically: the prerequisite goes first, though it is lower priority.
	f = &fakeAgent{do: func(AgentRequest) { s.AddCheck(dep, "test", "pass", "") }}
	rep, _ = Step(context.Background(), s, f, opts())
	if rep.TaskID != dep || !rep.Launched {
		t.Fatalf("picked: %+v", rep)
	}
}

// Commands an orchestrated agent never needs are denied outright, so an
// extra --allow cannot reopen them.
func TestToolsDenyTheStateChangingAclineCommands(t *testing.T) {
	_, deny := Tools(store.RouteStartWork, []string{"Bash(acline *)"})
	for _, must := range []string{
		"Bash(acline project add*)", "Bash(acline init*)", "Bash(acline check record*)",
		"Bash(acline session*)", "Bash(acline task update*)", "Bash(acline task done*)",
	} {
		if !slices.Contains(deny, must) {
			t.Errorf("deny list is missing %q: %v", must, deny)
		}
	}
}

func TestRunnerToolsAllowOnlyWhatAPersonConfigured(t *testing.T) {
	got := RunnerTools([]store.CheckRunner{{Kind: "test", Command: "npm  test"}, {Kind: "lint", Command: "sh -c (evil)"}, {Kind: "sca", Command: " "}})
	if !slices.Equal(got, []string{"Bash(npm test)", "Bash(npm test *)"}) {
		t.Fatalf("RunnerTools = %v", got)
	}
}

func TestStepAllowsTheProjectsRunners(t *testing.T) {
	s := newAgentStore(t)
	var pid int64
	var err error
	asPerson(s, func() {
		if pid, err = s.AddProject("web", "", ""); err == nil {
			err = s.SetCheckRunner(pid, "test", "npm test", "")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	readyTask(t, s, store.TaskOpts{ProjectID: &pid})
	f := &fakeAgent{}
	if _, err := Step(context.Background(), s, f, opts()); err != nil {
		t.Fatal(err)
	}
	if len(f.reqs) != 1 || !contains(f.reqs[0].AllowTools, "Bash(npm test *)") {
		t.Fatalf("the project's runner is not allowed: %v", f.reqs)
	}
}

// Another Claude session on the shared store being denied something is not
// this step's violation.
func TestStepIgnoresViolationsFromOtherSessions(t *testing.T) {
	s := newAgentStore(t)
	id := readyTask(t, s, store.TaskOpts{})
	f := &fakeAgent{do: func(AgentRequest) {
		s.AddCheck(id, "test", "pass", "")
		s.LogEvent(nil, nil, "guard_denied", "another session was denied Read(.env)")
	}}
	rep, err := Step(context.Background(), s, f, opts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stop == StopViolation || rep.Violations != 0 {
		t.Fatalf("a violation outside the step's session was counted: %+v", rep)
	}
}
