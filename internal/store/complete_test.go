package store

import (
	"errors"
	"testing"
)

func count(t *testing.T, s *Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCompleteTaskBlockedWritesNothing(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})

	_, err := s.CompleteTask(id, false, "")
	var blocked *GateBlockedError
	if !errors.As(err, &blocked) || len(blocked.Blockers) == 0 {
		t.Fatalf("want *GateBlockedError with blockers, got %v", err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM approvals`); n != 0 {
		t.Errorf("blocked completion wrote %d approval(s)", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events`); n != 0 {
		t.Errorf("blocked completion wrote %d event(s)", n)
	}
	if got, _ := s.GetTask(id); got.Status == "done" {
		t.Error("task was completed despite a blocked gate")
	}
}

func TestCompleteTaskForceRecordsOverrideAtomically(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})

	res, err := s.CompleteTask(id, true, "")
	if err != nil || !res.Overridden || len(res.Blockers) == 0 {
		t.Fatalf("CompleteTask(force) = %+v, %v", res, err)
	}
	if got, _ := s.GetTask(id); got.Status != "done" {
		t.Errorf("status = %q, want done", got.Status)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM approvals WHERE kind='override' AND decision='overridden'`); n != 1 {
		t.Errorf("override approvals = %d, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type='override'`); n != 1 {
		t.Errorf("override events = %d, want 1", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type='status_change'`); n != 1 {
		t.Errorf("status_change events = %d, want 1", n)
	}
	if r, err := s.VerifyChain(); err != nil || !r.OK() {
		t.Fatalf("chain: %+v, %v", r, err)
	}
}

// The failure this fixes: an override approval and event committed, then the
// status update failed, leaving an override on record for a task that never
// completed.
func TestCompleteTaskRollsBackEverythingWhenALaterStepFails(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_status BEFORE UPDATE OF status ON tasks BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CompleteTask(id, true, ""); err == nil {
		t.Fatal("expected the injected status-update failure to surface")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM approvals`); n != 0 {
		t.Errorf("override approval survived a failed completion (%d rows)", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events`); n != 0 {
		t.Errorf("audit events survived a failed completion (%d rows)", n)
	}
}

func TestCompleteTaskCleanPathStillWorks(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	res, err := s.CompleteTask(id, false, "")
	if err != nil || res.Overridden {
		t.Fatalf("CompleteTask = %+v, %v", res, err)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM approvals`); n != 0 {
		t.Errorf("clean completion wrote %d approval(s)", n)
	}
}

func TestAuditFailureIsReportedNotSwallowed(t *testing.T) {
	s := humanStore(t)
	var got string
	s.AuditErrorHandler = func(eventType string, err error) { got = eventType }
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_evt BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	s.LogEventGlobal("memory_recorded", "x")
	if got != "memory_recorded" {
		t.Errorf("AuditErrorHandler got %q, want the failed event type", got)
	}
}

func TestSetTaskStatusIsAtomicAndRefusesDone(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if err := s.SetTaskStatus(id, "done"); !errors.Is(err, ErrStatusDoneNeedsGate) {
		t.Fatalf("done = %v, want ErrStatusDoneNeedsGate", err)
	}
	if err := s.SetTaskStatus(id, "blocked"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetTask(id); got.Status != "blocked" {
		t.Errorf("status = %q", got.Status)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type='status_change'`); n != 1 {
		t.Errorf("status_change events = %d, want 1", n)
	}
	// a failure in the event insert must roll the status change back too
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_evt2 BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTaskStatus(id, "review"); err == nil {
		t.Fatal("expected the audit failure to surface")
	}
	if got, _ := s.GetTask(id); got.Status != "blocked" {
		t.Errorf("status changed to %q despite the failed audit write", got.Status)
	}
	if err := s.SetTaskStatus(id, "nonsense"); err == nil {
		t.Error("invalid status accepted")
	}
}

// The gate was evaluated just before the completion transaction, so a
// failing check recorded by another process in between was missed.
func TestCompletionNoticesAFailingCheckRecordedWhileTheGateRan(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	if _, err := h.AddCheck(id, "test", "pass", "green"); err != nil {
		t.Fatal(err)
	}
	fired := false
	afterGateEvaluated = func() {
		if !fired {
			fired = true // another process records a failure in the window
			if _, err := h.AddCheck(id, "test", "fail", "red"); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { afterGateEvaluated = nil })

	_, err := h.CompleteTask(id, false, "")
	var blocked *GateBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("CompleteTask = %v, want the gate to block on the failure recorded in the window", err)
	}
	if task, _ := h.GetTask(id); task.Status == "done" {
		t.Fatal("the task completed on a stale gate")
	}
}
