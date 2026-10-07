package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

// fakeAcline writes a script standing in for the acline binary.
func fakeAcline(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "acline")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func orchSession(t *testing.T, st *store.Store, self string) (*tuitest.Session, *cleanups) {
	t.Helper()
	m, err := newShell(st, nil, noHash, orchestratorScreen{})
	if err != nil {
		t.Fatal(err)
	}
	m.self, m.cleanup = self, &cleanups{}
	if m, err = m.reload(); err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 160, 40)
	t.Cleanup(s.Close)
	return s, m.cleanup
}

func TestStartTheOrchestratorAndSeeWhyItStopped(t *testing.T) {
	st := openTestStore(t)
	self := fakeAcline(t, `echo "args: $*"
echo "task #4  start_work  (role developer)"
echo "  stopped: needs_a_person — task #4 needs approval"
`)
	s, _ := orchSession(t, st, self)
	shows(t, s, "stop marker: clear", "enter starts")
	s.Keys("enter")
	shows(t, s, "Start the orchestrator", "Max steps", "Dry run")
	s.Keys("enter") // the defaults: one step, the next launchable task
	waitFor(t, s, "orchestrator finished")
	shows(t, s, "$ acline orchestrate step --step-budget 1.00 --timeout 15m", "args: --db "+st.Path+" orchestrate step",
		"task #4  start_work", "stopped: needs_a_person")
}

func TestRunWithLimitsForOneTask(t *testing.T) {
	st := openTestStore(t)
	s, _ := orchSession(t, st, fakeAcline(t, `echo "args: $*"`))
	s.Keys("enter")
	s.Keys("down") // mode: run
	s.Keys("tab", "7")
	s.Keys("tab", "backspace", "3") // max steps 3
	s.Keys("tab", "tab", "tab", "tab", "space", "enter")
	waitFor(t, s, "orchestrator finished")
	shows(t, s, "orchestrate run --step-budget 1.00 --timeout 15m --max-steps 3 --max-cost 5.00 --dry-run 7")
}

func TestTheStopMarkerAndEndingARunNow(t *testing.T) {
	st := openTestStore(t)
	self := fakeAcline(t, `trap 'echo "got interrupted"; exit 130' INT
echo "step running"
while :; do sleep 0.05; done
`)
	s, _ := orchSession(t, st, self)
	stop := filepath.Join(filepath.Dir(st.Path), "orchestrator.stop")

	s.Keys("S")
	shows(t, s, "stop set", "SET, nothing will launch")
	if _, err := os.Stat(stop); err != nil {
		t.Fatalf("the stop marker was not written: %v", err)
	}
	s.Keys("C")
	shows(t, s, "stop cleared", "stop marker: clear")

	s.Keys("enter")
	s.Keys("enter")
	waitFor(t, s, "step running")
	s.Keys("q") // a stray q while it runs does not quit
	if s.Done() {
		t.Fatal("q quit while the orchestrator ran")
	}
	s.Keys("K")
	waitFor(t, s, "got interrupted")
	waitFor(t, s, "orchestrator ended")
}

func TestQuittingEndsARunningOrchestrator(t *testing.T) {
	st := openTestStore(t)
	marker := filepath.Join(t.TempDir(), "ended")
	self := fakeAcline(t, `trap 'touch `+marker+`; exit 130' INT
echo "step running"
while :; do sleep 0.05; done
`)
	s, done := orchSession(t, st, self)
	s.Keys("enter")
	s.Keys("enter")
	waitFor(t, s, "step running")
	start := time.Now()
	done.run() // what Run does when the program exits
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the run was not interrupted on exit: %v", err)
	}
	if time.Since(start) > interruptGrace {
		t.Error("exit waited for the grace period instead of the run ending")
	}
	if !strings.Contains(rendered(s), "step running") {
		t.Error("lost the output")
	}
}
