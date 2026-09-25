package store

import "testing"

func TestEvaluateGateUsesLatestCheckPerKind(t *testing.T) {
	s := humanStore(t)
	taskID, err := s.AddTask("repair failing test", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCheck(taskID, "test", "fail", "first run"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCheck(taskID, "test", "pass", "fixed run"); err != nil {
		t.Fatal(err)
	}
	gate, err := s.EvaluateGate(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.OK() {
		t.Fatalf("later passing result should supersede failure: %+v", gate.Blockers)
	}
}

func TestEvaluateGateUsesLatestReviewDecision(t *testing.T) {
	s := humanStore(t)
	taskID, err := s.AddTask("reviewed task", "", "normal", TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCheck(taskID, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApproval(taskID, "code_review", "reviewer", "approved", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApproval(taskID, "code_review", "reviewer", "rejected", "regression found"); err != nil {
		t.Fatal(err)
	}
	gate, err := s.EvaluateGate(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if gate.OK() {
		t.Fatal("later rejection should revoke the earlier approval")
	}
}

func TestEvaluateGateIgnoresUnrelatedApprovalKind(t *testing.T) {
	s := humanStore(t)
	taskID, err := s.AddTask("reviewed task", "", "normal", TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}
	runnerCheck(t, s, taskID, "test", "pass", "") // high risk needs a check acline ran
	if _, err := s.AddApproval(taskID, "code_review", "reviewer", "approved", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApproval(taskID, "override", "reviewer", "overridden", "unrelated gate bypass"); err != nil {
		t.Fatal(err)
	}
	gate, err := s.EvaluateGate(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !gate.OK() {
		t.Fatalf("an unrelated approval kind must not revoke the code_review approval: %+v", gate.Blockers)
	}
}
