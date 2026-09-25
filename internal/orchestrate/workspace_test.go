package orchestrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/store"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintSeesEditsAndIgnoresNoise(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "add.go"), "a - b")
	base := fingerprint(dir)
	if base == "" || base != fingerprint(dir) {
		t.Fatalf("not stable: %q", base)
	}
	write(t, filepath.Join(dir, ".git", "index"), "noise")
	write(t, filepath.Join(dir, "node_modules", "x", "y.js"), "noise")
	if fingerprint(dir) != base {
		t.Error("changes under .git or node_modules counted")
	}
	// A same-length rewrite is a change; rewriting the same bytes is not.
	write(t, filepath.Join(dir, "add.go"), "a + b")
	if fingerprint(dir) == base {
		t.Error("an edit was not seen")
	}
	write(t, filepath.Join(dir, "add.go"), "a - b")
	if fingerprint(dir) != base {
		t.Error("restoring the original content counted as a change")
	}
	if fingerprint("") != "" {
		t.Error("empty dir should be unknown")
	}
	if workspaceChanged("", "x") || workspaceChanged("x", "") || workspaceChanged("x", "x") || !workspaceChanged("x", "y") {
		t.Error("workspaceChanged rules wrong")
	}
}

// The scenario from the first live run: the agent fixes the code but records
// nothing. That is progress (the next step, verify, records the check), not a stall.
func TestEditingFilesWithoutRecordingAnythingIsProgress(t *testing.T) {
	s := newAgentStore(t)
	readyTask(t, s, store.TaskOpts{})
	dir := t.TempDir()
	write(t, filepath.Join(dir, "add.go"), "return a - b")
	o := opts()
	o.Dir = dir
	f := &fakeAgent{do: func(AgentRequest) { write(t, filepath.Join(dir, "add.go"), "return a + b") }}
	rep, err := Step(context.Background(), s, f, o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Stop != StopNone || !rep.FilesEdited || rep.After != store.RouteVerify {
		t.Fatalf("report = %+v", rep)
	}
	// ...and a step that changes nothing at all still stalls, even with a Dir.
	s2 := newAgentStore(t)
	readyTask(t, s2, store.TaskOpts{})
	if rep, _ := Step(context.Background(), s2, &fakeAgent{}, o); rep.Stop != StopNoProgress || rep.FilesEdited {
		t.Fatalf("idle agent: %+v", rep)
	}
}

func TestDiagnosticsAreReportedAndLogged(t *testing.T) {
	s := newAgentStore(t)
	id := readyTask(t, s, store.TaskOpts{})
	f := &fakeAgent{
		res: AgentResult{Summary: "fixed add.go", Denied: []string{"Bash {\"command\":\"acline check run 1\"}"}, Turns: 4},
		do:  func(AgentRequest) { s.AddCheck(id, "test", "pass", "") },
	}
	rep, _ := Step(context.Background(), s, f, opts())
	if rep.Summary != "fixed add.go" || len(rep.Denied) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	evs, _ := s.ListEvents(&id, 20)
	logged := false
	for _, e := range evs {
		logged = logged || (e.Type == "dispatch" && strings.Contains(e.Message, "denied=1") && strings.Contains(e.Message, "acline check run 1") && strings.Contains(e.Message, "turns=4"))
	}
	if !logged {
		t.Errorf("denials not in the audit trail: %+v", evs)
	}
}

func TestParseReportToleratesAnyShape(t *testing.T) {
	cost, summary, denied, turns := parseReport(`{"total_cost_usd":0.14,"result":"done","num_turns":3,
		"permission_denials":[{"tool_name":"Bash","tool_input":{"command":"rm x"}},{"tool_name":"Bash","tool_input":{"command":"rm x"}},"plain string"]}`)
	if cost != 0.14 || summary != "done" || turns != 3 || len(denied) != 2 || !strings.HasPrefix(denied[0], "Bash ") {
		t.Errorf("got %v %q %v %d", cost, summary, denied, turns)
	}
	for _, junk := range []string{"", "not json", "[]", `{"result":5}`, `{"permission_denials":"x"}`} {
		parseReport(junk) // must not panic
	}
	if _, s, _, _ := parseReport(`{"result":"` + strings.Repeat("x", 2000) + `"}`); len(s) > maxSummary+5 {
		t.Errorf("summary not truncated: %d", len(s))
	}
}
