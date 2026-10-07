package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

// actionSession opens task #1 (high risk, a passing runner test on the code
// as it is now) in detail.
func actionSession(t *testing.T, opts store.TaskOpts) (*tuitest.Session, *store.Store) {
	t.Helper()
	st := openTestStore(t)
	id, err := st.AddTask("ship the login fix", "", "normal", opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddCheckWithMeta(id, nil, "test", "pass", "ran", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:now"}); err != nil {
		t.Fatal(err)
	}
	st.AddCriterion(id, "When a user logs in, the system shall return them to the page they came from")
	m, err := newShell(st, nil, func(string) string { return "sha256:now" }, tasksScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 160, 40)
	t.Cleanup(s.Close)
	s.Keys("enter")
	return s, st
}

func approvals(t *testing.T, st *store.Store) []store.Approval {
	t.Helper()
	a, err := st.ListApprovals(1)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestApproveFromTheDetail(t *testing.T) {
	s, st := actionSession(t, store.TaskOpts{Risk: "high"})
	shows(t, s, "(person) human approval required", "[a] approve")
	s.Keys("a")
	shows(t, s, "Approve #1 for the code as it is now?")
	s.Keys("y")
	shows(t, s, "approval #1 recorded for task #1", "Gate: can be completed", "approved code_review by")
	if a := approvals(t, st); len(a) != 1 || a[0].TreeHash.String != "sha256:now" {
		t.Fatalf("approvals = %+v, want one bound to the current tree", a)
	}
}

func TestEscCancelsAnActionAndWritesNothing(t *testing.T) {
	s, st := actionSession(t, store.TaskOpts{Risk: "high"})
	s.Keys("a", "esc")
	lacks(t, s, "Approve #1")
	s.Keys("D", "n")
	if a := approvals(t, st); len(a) != 0 {
		t.Fatalf("a cancelled approval was recorded: %+v", a)
	}
	if task, _ := st.GetTask(1); task.Status == "done" {
		t.Fatal("a declined force completed the task")
	}
}

// With a token enabled the store asks for it; the TUI asks the person on its
// own screen, masked, and the token never appears in a frame.
func TestTheTokenIsAskedForMaskedAndNeverShown(t *testing.T) {
	s, st := actionSession(t, store.TaskOpts{Risk: "high"})
	token, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	s.Keys("a", "y")
	shows(t, s, "Approval token for approving task #1", "hidden")
	if len(approvals(t, st)) != 0 {
		t.Fatal("approved before the token was given")
	}

	s.Keys("wrong-token", "enter")
	shows(t, s, "invalid approval token")
	if len(approvals(t, st)) != 0 {
		t.Fatal("a wrong token approved the task")
	}

	s.Keys("a", "y")
	s.Keys(token)
	if strings.Contains(rendered(s), token) || strings.Contains(rendered(s), token[:8]) {
		t.Fatalf("the token is visible while typed:\n%s", rendered(s))
	}
	s.Keys("enter")
	shows(t, s, "approval #1 recorded")
	if strings.Contains(rendered(s), token[:8]) {
		t.Fatal("the token is visible after use")
	}
	if len(approvals(t, st)) != 1 {
		t.Fatal("the right token did not approve")
	}
}

func TestRejectRecordsTheReason(t *testing.T) {
	s, st := actionSession(t, store.TaskOpts{Risk: "high"})
	s.Keys("X", "redirect still loops on Safari", "enter")
	shows(t, s, "rejection #1 recorded", "rejected code_review", "redirect still loops on Safari")
	if a := approvals(t, st); len(a) != 1 || a[0].Decision != "rejected" {
		t.Fatalf("approvals = %+v", a)
	}
}

func TestDoneIsGatedAndForceIsAnOverride(t *testing.T) {
	s, st := actionSession(t, store.TaskOpts{Risk: "high"})
	s.Keys("d", "y")
	shows(t, s, "gate not satisfied", "human approval required")
	if task, _ := st.GetTask(1); task.Status == "done" {
		t.Fatal("done ignored the gate")
	}
	s.Keys("D", "y")
	shows(t, s, "forced done", "Gate: done")
}

func TestCriterionDeferAndRiskFromTheDetail(t *testing.T) {
	s, st := actionSession(t, store.TaskOpts{Risk: "high"})
	s.Keys("c", "enter")
	shows(t, s, "criterion #1 checked", "[x] #1 When a user logs in")
	s.Keys("z", "waiting on the auth vendor", "enter")
	shows(t, s, "deferred  waiting on the auth vendor")
	s.Keys("R", "home", "enter") // low
	shows(t, s, "risk      low")
	if task, _ := st.GetTask(1); task.Risk != "low" || !task.Deferred {
		t.Fatalf("task = %+v", task)
	}
}

func TestTypingInAPromptDoesNotTriggerShortcuts(t *testing.T) {
	s, _ := actionSession(t, store.TaskOpts{Risk: "high"})
	s.Keys("X", "q2p", "esc")
	if s.Done() {
		t.Fatal("q typed into a prompt quit the TUI")
	}
	shows(t, s, "[Tasks]", "#1 ship the login fix")
}

// checkSession opens task #1 of a project whose test runner is command.
func checkSession(t *testing.T, command string) (*tuitest.Session, *store.Store) {
	t.Helper()
	st := openTestStore(t)
	dir := t.TempDir()
	pid, err := st.AddProject("api", dir, "hotl")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCheckRunner(pid, "test", command, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTask("ship it", "", "normal", store.TaskOpts{ProjectID: &pid}); err != nil {
		t.Fatal(err)
	}
	m, err := newShell(st, nil, func(string) string { return "sha256:now" }, tasksScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 140, 30)
	t.Cleanup(s.Close)
	s.Keys("enter")      // opening the task takes two messages: let them land first
	s.Keys("t", "enter") // test is the default choice
	return s, st
}

func waitFor(t *testing.T, s *tuitest.Session, want string) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		s.Advance(0)
		if strings.Contains(rendered(s), want) {
			return
		}
	}
	t.Fatalf("screen never showed %q:\n%s", want, rendered(s))
}

func TestRunACheckFromTheDetailAndRecordIt(t *testing.T) {
	s, st := checkSession(t, "echo all 12 tests passed")
	waitFor(t, s, "test check #1: pass")
	shows(t, s, "test         pass    runner")
	checks, _ := st.ListChecks(1)
	if len(checks) != 1 || checks[0].Source != store.CheckSourceRunner || checks[0].TreeHash.String != "sha256:now" {
		t.Fatalf("checks = %+v, want one runner check on the current tree", checks)
	}
	if !strings.Contains(checks[0].Detail.String, "all 12 tests passed") {
		t.Errorf("detail = %q", checks[0].Detail.String)
	}
}

func TestStoppingACheckRecordsNothing(t *testing.T) {
	s, st := checkSession(t, "sleep 30")
	waitFor(t, s, "Running the test check")
	s.Keys("q") // a stray q while it runs is not quit
	if s.Done() {
		t.Fatal("q quit while a check ran")
	}
	s.Keys("esc")
	waitFor(t, s, "test check cancelled; nothing was recorded")
	if checks, _ := st.ListChecks(1); len(checks) != 0 {
		t.Fatalf("a stopped run was recorded: %+v", checks)
	}
}
