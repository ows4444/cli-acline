package orchestrate

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/store"
)

func specOpts(idea string) SpecOptions {
	return SpecOptions{Idea: idea, StepBudgetUSD: 1, StepTimeout: opts().StepTimeout}
}

func TestSpecToolsAreReadOnlyPlusTheSingleSubmitCommand(t *testing.T) {
	allow, deny := SpecTools(nil)
	for _, must := range []string{"Read", "Grep", "Glob", "Bash(acline spec add *)"} {
		if !contains(allow, must) {
			t.Errorf("allow lacks %q: %v", must, allow)
		}
	}
	for _, bad := range []string{"Edit", "Write", "NotebookEdit", "acline spec approve", "acline spec revise"} {
		if strings.Contains(strings.Join(allow, ","), bad) {
			t.Errorf("a spec-drafting run may not be allowed %q: %v", bad, allow)
		}
	}
	for _, must := range []string{"Edit", "Write", "NotebookEdit", "Bash(acline spec approve*)", "Bash(acline spec revise*)",
		"Bash(acline approve*)", "Bash(acline auth*)", "Bash(git push*)"} {
		if !contains(deny, must) {
			t.Errorf("deny lacks %q", must)
		}
	}
}

func TestSpecPromptCarriesTheContextTheAgentNeeds(t *testing.T) {
	s := newAgentStore(t)
	s.AddSpec("Existing thing", "already here")
	m, _ := s.AddMemory("store", "pitfall", "cache entries need a ttl")
	personView(s).ReviewMemory(m, true, "")
	s.AddMemory("store", "pitfall", "an unreviewed claim must not reach the prompt")
	d, _ := s.AddDecision("use sqlite", store.DecisionOpts{Decision: "SQLite is the only store"})
	personView(s).AcceptDecision(d, "")
	p, err := SpecPrompt(s, "let users export their data as CSV", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Draft a spec from an idea",
		"export their data as CSV",
		"Existing specs", "#1 Existing thing",
		"Accepted decisions", "SQLite is the only store",
		"Lessons from earlier work", "cache entries need a ttl",
		"Role contract: designer", "You are read-only",
		"acline spec add \"<a short title>\" --body-file - <<'SPEC'",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(p, "unreviewed claim") {
		t.Error("pending memory reached the spec-drafting prompt")
	}
}

func TestSpecPromptTruncatesALongIdea(t *testing.T) {
	s := newAgentStore(t)
	p, _ := SpecPrompt(s, strings.Repeat("x", maxIdeaLen+500), nil)
	if !strings.Contains(p, "truncated") {
		t.Error("a long idea was not truncated")
	}
}

func TestDraftSpecSucceedsWhenTheAgentProposesOne(t *testing.T) {
	t.Setenv("ACLINE_APPROVAL_TOKEN", "must-not-leak")
	s := newAgentStore(t)
	f := &fakeAgent{
		res: AgentResult{CostUSD: 0.3, Summary: "drafted a CSV export spec", Turns: 4},
		do: func(AgentRequest) {
			if _, err := s.AddSpec("CSV export", "Let users export their data as CSV."); err != nil {
				t.Errorf("the fake agent's spec add failed: %v", err)
			}
		},
	}
	rep, err := DraftSpec(context.Background(), s, f, specOpts("let users export their data as CSV"))
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Launched || rep.Stop != StopNone || rep.SpecID == 0 || rep.CostUSD != 0.3 || rep.Summary != "drafted a CSV export spec" {
		t.Fatalf("report = %+v", rep)
	}
	req := f.reqs[0]
	if !strings.Contains(req.Prompt, "export their data as CSV") || contains(req.AllowTools, "Edit") || !contains(req.AllowTools, "Bash(acline spec add *)") {
		t.Errorf("request = %+v", req)
	}
	if strings.Contains(strings.Join(req.Env, "\n"), "must-not-leak") || !contains(req.Env, "ACLINE_ACTOR_TYPE=agent") || !contains(req.Env, "ACLINE_ROLE=designer") {
		t.Errorf("agent env = %v", req.Env)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("the run left its session open")
	}
	sp, _ := s.GetSpec(rep.SpecID)
	if sp.Status != "draft" {
		t.Errorf("spec = %+v", sp)
	}
	if n := countTasks(t, s); n != 0 {
		t.Errorf("drafting a spec created %d task(s)", n)
	}
	if plans, _ := s.ListPlans(&rep.SpecID, ""); len(plans) != 0 {
		t.Errorf("drafting a spec created a plan: %+v", plans)
	}
	evs, _ := s.QueryEvents(store.EventFilter{Limit: 50})
	var launches, finishes int
	for _, e := range evs {
		if e.Type == "dispatch" {
			launches += strings.Count(e.Message, "launch spec drafting")
			finishes += strings.Count(e.Message, "finished spec drafting") * strings.Count(e.Message, "spec="+itoa(rep.SpecID))
		}
	}
	if launches != 1 || finishes != 1 {
		t.Errorf("dispatch events launch=%d finish=%d", launches, finishes)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}

func TestDraftSpecDoesNotLaunchWhenItShouldNot(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(s *store.Store) SpecOptions
		want Stop
	}{
		{"empty idea", func(s *store.Store) SpecOptions { return specOpts("   ") }, ""},
		{"session already active", func(s *store.Store) SpecOptions {
			s.BeginSession(store.SessionStart{})
			return specOpts("an idea")
		}, StopSessionActive},
		{"stop file", func(s *store.Store) SpecOptions {
			o := specOpts("an idea")
			o.StopFile = filepath.Join(t.TempDir(), "stop")
			writeFile(t, o.StopFile)
			return o
		}, StopKilled},
	} {
		s := newAgentStore(t)
		o := c.set(s)
		f := &fakeAgent{}
		rep, err := DraftSpec(context.Background(), s, f, o)
		if c.name == "empty idea" {
			if err == nil {
				t.Error("empty idea: expected an error")
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rep.Stop != c.want || rep.Launched || f.calls != 0 {
			t.Errorf("%s: stop=%q launched=%t calls=%d (%s)", c.name, rep.Stop, rep.Launched, f.calls, rep.Detail)
		}
	}
	human := newAgentStore(t)
	human.Actor = store.Actor{Type: "human", ID: "me"}
	if _, err := DraftSpec(context.Background(), human, &fakeAgent{}, specOpts("an idea")); err == nil {
		t.Error("a human actor was accepted")
	}
}

func TestDraftSpecStopsWhenNoSpecWasProposed(t *testing.T) {
	for _, c := range []struct {
		name string
		res  AgentResult
		want Stop
	}{
		{"finished without proposing", AgentResult{}, StopNoProgress},
		{"failed", AgentResult{ExitCode: 2}, StopAgentFailed},
		{"timed out", AgentResult{ExitCode: -1, TimedOut: true}, StopAgentFailed},
	} {
		s := newAgentStore(t)
		rep, _ := DraftSpec(context.Background(), s, &fakeAgent{res: c.res}, specOpts("an idea"))
		if rep.Stop != c.want || rep.SpecID != 0 {
			t.Errorf("%s: %+v", c.name, rep)
		}
		notes, _ := s.ListNotes(store.NoteFilter{UnpromotedOnly: true})
		if len(notes) != 1 || !strings.Contains(notes[0].Body, "spec drafting stopped") {
			t.Errorf("%s: notes = %+v", c.name, notes)
		}
	}
}

func TestDraftSpecFlagsAnyFileChangeAndPolicyDenialEvenWithASpec(t *testing.T) {
	s := newAgentStore(t)
	dir := t.TempDir()
	write(t, filepath.Join(dir, "main.go"), "package main")
	o := specOpts("an idea")
	o.Dir = dir
	f := &fakeAgent{do: func(AgentRequest) {
		s.AddSpec("a spec", "body")
		write(t, filepath.Join(dir, "main.go"), "package main // edited by a read-only agent")
	}}
	rep, _ := DraftSpec(context.Background(), s, f, o)
	if rep.Stop != StopViolation || rep.SpecID == 0 || !strings.Contains(rep.Detail, "read-only") {
		t.Fatalf("file change: %+v", rep)
	}

	s2 := newAgentStore(t)
	f2 := &fakeAgent{do: func(AgentRequest) {
		s2.AddSpec("a spec", "body")
		s2.LogEventGlobal("policy_violation", "denied Bash")
	}}
	if rep, _ := DraftSpec(context.Background(), s2, f2, specOpts("an idea")); rep.Stop != StopViolation || rep.SpecID == 0 {
		t.Fatalf("policy denial: %+v", rep)
	}
}

func TestDraftSpecDryRunLaunchesNothingAndShowsTheCommandAndPrompt(t *testing.T) {
	s := newAgentStore(t)
	o := specOpts("an idea")
	o.DryRun = true
	rep, err := DraftSpec(context.Background(), s, ClaudeAgent{}, o)
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Join(rep.Command, " ")
	allowed := cmd[strings.Index(cmd, "--allowedTools"):strings.Index(cmd, "--disallowedTools")]
	if rep.Stop != StopDryRun || rep.Launched || !strings.Contains(cmd, "--permission-mode dontAsk") || !strings.Contains(allowed, "Bash(acline spec add *)") ||
		strings.Contains(allowed, "Edit") || strings.Contains(allowed, "Write") || !strings.Contains(rep.Prompt, "Draft a spec from an idea") {
		t.Fatalf("report = %+v", rep)
	}
	if _, err := s.CurrentSession(); err == nil {
		t.Error("a dry run began a session")
	}
	if n := countTasks(t, s); n != 0 {
		t.Errorf("dry run created %d task(s)", n)
	}
}
