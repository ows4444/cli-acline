package orchestrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/store"
)

// approvedSpecFor makes an approved spec with no plan or tasks, the only kind a
// planning run is willing to launch for.
func approvedSpecFor(t *testing.T, s *store.Store) int64 {
	t.Helper()
	id, err := s.AddSpec("Inventory", "Track stock levels. Support reservations.")
	if err != nil {
		t.Fatal(err)
	}
	if err := personView(s).ApproveSpec(id, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func planOpts(spec int64) PlanOptions {
	return PlanOptions{SpecID: spec, StepBudgetUSD: 1, StepTimeout: opts().StepTimeout}
}

func TestPlanToolsAreReadOnlyPlusTheSingleSubmitCommand(t *testing.T) {
	allow, deny := PlanTools(nil)
	for _, must := range []string{"Read", "Grep", "Glob", "Bash(acline plan propose *)"} {
		if !contains(allow, must) {
			t.Errorf("allow lacks %q: %v", must, allow)
		}
	}
	for _, bad := range []string{"Edit", "Write", "NotebookEdit", "Bash(go", "Bash(git", "acline check run", "acline plan approve"} {
		if strings.Contains(strings.Join(allow, ","), bad) {
			t.Errorf("a planning run may not be allowed %q: %v", bad, allow)
		}
	}
	for _, must := range []string{"Edit", "Write", "NotebookEdit", "Bash(acline plan approve*)", "Bash(acline plan edit*)",
		"Bash(acline plan reject*)", "Bash(acline plan revise*)", "Bash(acline approve*)", "Bash(acline auth*)", "Bash(git push*)"} {
		if !contains(deny, must) {
			t.Errorf("deny lacks %q", must)
		}
	}
}

func TestPlanPromptCarriesTheContextTheAgentNeeds(t *testing.T) {
	s := newAgentStore(t)
	spec := approvedSpecFor(t, s)
	s.AddTask("existing work", "", "normal", store.TaskOpts{})
	// An agent's memory is pending until a person reviews it, and pending memory
	// is never trusted context, so approve this one as a reviewer would.
	m, _ := s.AddMemory("store", "pitfall", "cache entries need a ttl")
	personView(s).ReviewMemory(m, true, "")
	s.AddMemory("store", "pitfall", "an unreviewed claim must not reach the prompt")
	d, _ := s.AddDecision("use sqlite", store.DecisionOpts{Decision: "SQLite is the only store"})
	personView(s).AcceptDecision(d, "")
	sp, _ := s.GetSpec(spec)
	p, err := PlanPrompt(s, sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Propose a plan for spec #1: Inventory", "Support reservations.",
		"Accepted decisions", "SQLite is the only store",
		"Open tasks that already exist", "existing work",
		"Lessons from earlier work", "cache entries need a ttl",
		"Role contract: architect", "You are read-only",
		"acline plan propose 1 --file - <<'PLAN'", "cannot grant auto", "EARS",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(p, "unreviewed claim") {
		t.Error("pending memory reached the planning prompt")
	}
	if strings.Contains(p, "\n# Role:") {
		t.Errorf("the embedded role contract's H1 was not demoted below the prompt's own headings")
	}
}

func TestPlanRunSucceedsWhenTheAgentProposesADraft(t *testing.T) {
	t.Setenv("ACLINE_APPROVAL_TOKEN", "must-not-leak")
	s := newAgentStore(t)
	spec := approvedSpecFor(t, s)
	f := &fakeAgent{
		res: AgentResult{CostUSD: 0.4, Summary: "5 tasks", Turns: 6},
		do: func(AgentRequest) {
			if _, err := s.ProposePlan(spec, store.PlanInput{Items: []store.PlanItemInput{{Ref: "T1", Title: "one"}, {Ref: "T2", Title: "two", DependsOn: []string{"T1"}}}}); err != nil {
				t.Errorf("the fake agent's proposal failed: %v", err)
			}
		},
	}
	rep, err := Plan(context.Background(), s, f, planOpts(spec))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Launched || rep.Stop != StopNone || rep.PlanID == 0 || rep.CostUSD != 0.4 || rep.Summary != "5 tasks" {
		t.Fatalf("report = %+v", rep)
	}
	req := f.reqs[0]
	if !strings.Contains(req.Prompt, "Propose a plan for spec #1") || contains(req.AllowTools, "Edit") || !contains(req.AllowTools, "Bash(acline plan propose *)") {
		t.Errorf("request = %+v", req)
	}
	if strings.Contains(strings.Join(req.Env, "\n"), "must-not-leak") || !contains(req.Env, "ACLINE_ACTOR_TYPE=agent") || !contains(req.Env, "ACLINE_ROLE=architect") {
		t.Errorf("agent env = %v", req.Env)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("the run left its session open")
	}
	// The draft exists, and NOTHING was approved or created.
	if p, _ := s.GetPlan(rep.PlanID); p.Status != "draft" {
		t.Errorf("plan = %+v", p)
	}
	if n := countTasks(t, s); n != 0 {
		t.Errorf("a planning run created %d task(s)", n)
	}
	evs, _ := s.QueryEvents(store.EventFilter{Limit: 50})
	var launches, finishes int
	for _, e := range evs {
		if e.Type == "dispatch" {
			launches += strings.Count(e.Message, "launch planning")
			finishes += strings.Count(e.Message, "finished planning") * strings.Count(e.Message, "plan=")
		}
	}
	if launches != 1 || finishes != 1 {
		t.Errorf("dispatch events launch=%d finish=%d", launches, finishes)
	}
}

func countTasks(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPlanRunDoesNotLaunchWhenItShouldNot(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(s *store.Store) (PlanOptions, int64)
		want Stop
	}{
		{"spec not approved", func(s *store.Store) (PlanOptions, int64) {
			id, _ := s.AddSpec("draft", "x")
			return planOpts(id), id
		}, StopNotPlannable},
		{"a draft plan already exists", func(s *store.Store) (PlanOptions, int64) {
			id := approvedSpecFor(t, s)
			s.ProposePlan(id, store.PlanInput{Items: []store.PlanItemInput{{Ref: "A", Title: "a"}}})
			return planOpts(id), id
		}, StopPlanExists},
		{"the spec already has tasks", func(s *store.Store) (PlanOptions, int64) {
			id := approvedSpecFor(t, s)
			s.AddTask("by hand", "", "normal", store.TaskOpts{SpecID: &id})
			return planOpts(id), id
		}, StopNotPlannable},
		{"session already active", func(s *store.Store) (PlanOptions, int64) {
			id := approvedSpecFor(t, s)
			s.BeginSession(store.SessionStart{})
			return planOpts(id), id
		}, StopSessionActive},
		{"stop file", func(s *store.Store) (PlanOptions, int64) {
			id := approvedSpecFor(t, s)
			o := planOpts(id)
			o.StopFile = filepath.Join(t.TempDir(), "stop")
			writeFile(t, o.StopFile)
			return o, id
		}, StopKilled},
	} {
		s := newAgentStore(t)
		o, _ := c.set(s)
		f := &fakeAgent{}
		rep, err := Plan(context.Background(), s, f, o)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rep.Stop != c.want || rep.Launched || f.calls != 0 {
			t.Errorf("%s: stop=%q launched=%t calls=%d (%s)", c.name, rep.Stop, rep.Launched, f.calls, rep.Detail)
		}
	}
	if _, err := Plan(context.Background(), newAgentStore(t), &fakeAgent{}, planOpts(99)); err == nil {
		t.Error("planning a missing spec did not error")
	}
	human := newAgentStore(t)
	human.Actor = store.Actor{Type: "human", ID: "me"}
	if _, err := Plan(context.Background(), human, &fakeAgent{}, planOpts(1)); err == nil {
		t.Error("a human actor was accepted")
	}
}

func TestPlanRunStopsWhenNoPlanWasProposed(t *testing.T) {
	for _, c := range []struct {
		name string
		res  AgentResult
		want Stop
	}{
		{"finished without proposing", AgentResult{}, StopNoProgress},
		{"failed", AgentResult{ExitCode: 2}, StopAgentFailed},
		{"timed out", AgentResult{ExitCode: -1, TimedOut: true}, StopAgentFailed},
		{"could not start", AgentResult{ExitCode: -1, StartErr: context.DeadlineExceeded}, StopAgentFailed},
	} {
		s := newAgentStore(t)
		spec := approvedSpecFor(t, s)
		rep, _ := Plan(context.Background(), s, &fakeAgent{res: c.res}, planOpts(spec))
		if rep.Stop != c.want || rep.PlanID != 0 {
			t.Errorf("%s: %+v", c.name, rep)
		}
		notes, _ := s.ListNotes(store.NoteFilter{UnpromotedOnly: true})
		if len(notes) != 1 || !strings.Contains(notes[0].Body, "planning for spec #1 stopped") {
			t.Errorf("%s: notes = %+v", c.name, notes)
		}
		if _, err := s.CurrentSession(); err == nil {
			t.Errorf("%s: session left open", c.name)
		}
	}
}

func TestPlanRunFlagsAnyFileChangeAndPolicyDenialEvenWithAPlan(t *testing.T) {
	s := newAgentStore(t)
	spec := approvedSpecFor(t, s)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), "package main")
	o := planOpts(spec)
	o.Dir = dir
	f := &fakeAgent{do: func(AgentRequest) {
		s.ProposePlan(spec, store.PlanInput{Items: []store.PlanItemInput{{Ref: "A", Title: "a"}}})
		write(t, filepath.Join(dir, "main.go"), "package main // edited by a read-only agent")
	}}
	rep, _ := Plan(context.Background(), s, f, o)
	if rep.Stop != StopViolation || rep.PlanID == 0 || !strings.Contains(rep.Detail, "read-only") {
		t.Fatalf("file change: %+v", rep)
	}

	s2 := newAgentStore(t)
	spec2 := approvedSpecFor(t, s2)
	f2 := &fakeAgent{do: func(AgentRequest) {
		s2.ProposePlan(spec2, store.PlanInput{Items: []store.PlanItemInput{{Ref: "A", Title: "a"}}})
		s2.LogEventGlobal("policy_violation", "denied Bash")
	}}
	if rep, _ := Plan(context.Background(), s2, f2, planOpts(spec2)); rep.Stop != StopViolation || rep.PlanID == 0 {
		t.Fatalf("policy denial: %+v", rep)
	}
}

func TestPlanDryRunLaunchesNothingAndShowsTheCommandAndPrompt(t *testing.T) {
	s := newAgentStore(t)
	spec := approvedSpecFor(t, s)
	o := planOpts(spec)
	o.DryRun = true
	rep, err := Plan(context.Background(), s, ClaudeAgent{}, o)
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(rep.Command, " ")
	allowed := cmd[strings.Index(cmd, "--allowedTools"):strings.Index(cmd, "--disallowedTools")]
	if rep.Stop != StopDryRun || rep.Launched || !strings.Contains(cmd, "--permission-mode dontAsk") || !strings.Contains(allowed, "Bash(acline plan propose *)") ||
		strings.Contains(allowed, "Edit") || strings.Contains(allowed, "Write") || !strings.Contains(rep.Prompt, "Propose a plan for spec #1") {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("a dry run began a session")
	}
}
