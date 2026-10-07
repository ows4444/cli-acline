package store

import (
	"errors"
	"testing"
)

// Every status a kind can hold has a row, and every row moves only to real statuses.
func TestTransitionTablesCoverEveryStatus(t *testing.T) {
	valid := map[string]map[string]bool{
		"task": ValidStatuses, "spec": ValidSpecStatuses, "decision": ValidDecisionStatuses,
		"plan": {"draft": true, "approved": true, "rejected": true, "superseded": true},
	}
	for kind, statuses := range valid {
		table := transitions[kind].next
		for st := range statuses {
			if _, ok := table[st]; !ok {
				t.Errorf("%s status %q has no transition row", kind, st)
			}
		}
		for from, tos := range table {
			if !statuses[from] {
				t.Errorf("%s table has a row for unknown status %q", kind, from)
			}
			for _, to := range tos {
				if !statuses[to] {
					t.Errorf("%s %s -> %q: not a status", kind, from, to)
				}
			}
		}
	}
}

func TestARejectedOrSupersededDecisionCannotBeAcceptedAgain(t *testing.T) {
	s := humanStore(t)
	rejected, _ := s.AddDecision("use widgets", DecisionOpts{})
	if err := s.RejectDecision(rejected, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptDecision(rejected, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("accepting a rejected decision = %v, want ErrInvalidTransition", err)
	}

	old, _ := s.AddDecision("v1", DecisionOpts{})
	repl, _ := s.AddDecision("v2", DecisionOpts{})
	if err := s.AcceptDecision(old, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SupersedeDecision(old, repl, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptDecision(old, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("accepting a superseded decision = %v, want ErrInvalidTransition", err)
	}
	if err := s.RejectDecision(old, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("rejecting a superseded decision = %v, want ErrInvalidTransition", err)
	}
	if d, _ := s.GetDecision(old); d.Status != "superseded" || !d.SupersededBy.Valid || d.SupersededBy.Int64 != repl {
		t.Fatalf("superseded decision changed: %+v", d)
	}

	// Accepting an accepted decision is still a harmless repeat.
	if err := s.AcceptDecision(repl, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptDecision(repl, ""); err != nil {
		t.Fatalf("accepting twice = %v", err)
	}
}

func TestOnlyADraftOrApprovedSpecCanBeApproved(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddSpec("s", "b")
	if err := s.ApproveSpec(id, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveSpec(id, ""); err != nil {
		t.Fatalf("approving twice = %v", err)
	}
	for _, status := range []string{"superseded", "implemented"} {
		if _, err := s.DB.Exec(`UPDATE specs SET status = ? WHERE id = ?`, status, id); err != nil {
			t.Fatal(err)
		}
		if err := s.ApproveSpec(id, ""); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("approving a %s spec = %v, want ErrInvalidTransition", status, err)
		}
	}
}

func TestADoneTaskIsNotCompletedAgainButCanBeReopened(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.CompleteTask(id, true, ""); err != nil {
		t.Fatal(err)
	}
	events := countRows(t, s, `SELECT COUNT(*) FROM events WHERE type = 'status_change'`)
	overrides := countRows(t, s, `SELECT COUNT(*) FROM approvals WHERE decision = 'overridden'`)
	if _, err := s.CompleteTask(id, true, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("completing a done task = %v, want ErrInvalidTransition", err)
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM events WHERE type = 'status_change'`); n != events {
		t.Errorf("a refused completion recorded a status change")
	}
	if n := countRows(t, s, `SELECT COUNT(*) FROM approvals WHERE decision = 'overridden'`); n != overrides {
		t.Errorf("a refused completion recorded a gate override")
	}
	if err := s.SetTaskStatus(id, "cancelled"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cancelling a done task = %v, want ErrInvalidTransition", err)
	}
	if err := s.SetTaskStatus(id, "in_progress"); err != nil {
		t.Fatalf("reopening a done task = %v", err)
	}
}

func TestACancelledTaskComesBackAsPlannedWork(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if err := s.SetTaskStatus(id, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginSession(SessionStart{TaskID: &id}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("starting a session on a cancelled task = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.UpdateTask(id, TaskUpdate{Status: "in_progress"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cancelled -> in_progress = %v, want ErrInvalidTransition", err)
	}
	if err := s.SetTaskStatus(id, "todo"); err != nil {
		t.Fatalf("cancelled -> todo = %v", err)
	}
}

func TestADecidedPlanCannotBeDecidedAgain(t *testing.T) {
	s := humanStore(t)
	spec := approvedSpec(t, s)
	id, _ := s.ProposePlan(spec, samplePlan())
	if err := s.RejectPlan(id, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApprovePlan(id, ""); err == nil {
		t.Fatal("approved a rejected plan")
	}
	if err := s.RejectPlan(id, ""); err == nil {
		t.Fatal("rejected a plan twice")
	}
}
