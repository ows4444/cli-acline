package store

import (
	"errors"
	"testing"
)

// acceptedDecision records a decision and accepts it as a person.
func acceptedDecision(t *testing.T, h *Store, title string) int64 {
	t.Helper()
	id, err := h.AddDecision(title, DecisionOpts{Decision: title})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.AcceptDecision(id, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func decisionStatus(t *testing.T, s *Store, id int64) string {
	t.Helper()
	d, err := s.GetDecision(id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Status
}

func TestAgentCannotRejectOrSupersedeAnAcceptedDecision(t *testing.T) {
	h := humanStore(t)
	old := acceptedDecision(t, h, "use postgres")
	a := asAgent(h)
	replacement, err := a.AddDecision("use sqlite", DecisionOpts{Decision: "use sqlite"})
	if err != nil {
		t.Fatal(err)
	}

	if err := a.RejectDecision(old, ""); !errors.Is(err, ErrAgentCannotRetireDecision) {
		t.Fatalf("agent reject = %v, want ErrAgentCannotRetireDecision", err)
	}
	if err := a.SupersedeDecision(old, replacement, ""); !errors.Is(err, ErrAgentCannotRetireDecision) {
		t.Fatalf("agent supersede = %v, want ErrAgentCannotRetireDecision", err)
	}
	if got := decisionStatus(t, h, old); got != "accepted" {
		t.Fatalf("status after refusals = %s", got)
	}

	if err := h.SupersedeDecision(old, replacement, ""); err != nil {
		t.Fatalf("person supersede = %v", err)
	}
	if got := decisionStatus(t, h, old); got != "superseded" {
		t.Fatalf("status = %s, want superseded", got)
	}
}

func TestAgentMayRejectOrSupersedeAProposedDecision(t *testing.T) {
	a := asAgent(humanStore(t))
	first, _ := a.AddDecision("draft one", DecisionOpts{})
	second, _ := a.AddDecision("draft two", DecisionOpts{})
	third, _ := a.AddDecision("draft three", DecisionOpts{})
	if err := a.RejectDecision(first, ""); err != nil {
		t.Fatalf("reject proposed = %v", err)
	}
	if err := a.SupersedeDecision(second, third, ""); err != nil {
		t.Fatalf("supersede proposed = %v", err)
	}
	if err := a.SupersedeDecision(third, third, ""); err == nil {
		t.Fatal("a decision superseded itself")
	}
}

func TestAgentRetiresAnAcceptedDecisionWithTheToken(t *testing.T) {
	h := humanStore(t)
	id := acceptedDecision(t, h, "use postgres")
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	a := asAgent(h)
	if err := a.RejectDecision(id, "wrong"); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := a.RejectDecision(id, tok); err != nil {
		t.Fatalf("correct token refused: %v", err)
	}
	if got := decisionStatus(t, h, id); got != "rejected" {
		t.Fatalf("status = %s", got)
	}
}

func TestAgentCannotPromoteOnAnEvalItRecorded(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Autonomy: "hitl"})
	a := asAgent(h)
	if _, err := a.AddEval(&id, nil, "suite", 1.0, 100, "typed, not measured"); err != nil {
		t.Fatal(err)
	}
	if err := a.PromoteAutonomy(id, "auto", "suite", 0.9); !errors.Is(err, ErrAgentCannotSelfPromote) {
		t.Fatalf("self-promotion = %v, want ErrAgentCannotSelfPromote", err)
	}
	if task, _ := h.GetTask(id); task.Autonomy != "hitl" {
		t.Fatalf("autonomy = %s", task.Autonomy)
	}
	// A person may still promote on it: the eval is evidence, the person decides.
	if err := h.PromoteAutonomy(id, "auto", "suite", 0.9); err != nil {
		t.Fatalf("person promotion = %v", err)
	}
}

func TestAgentCannotLowerThePromotionThreshold(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Autonomy: "hitl"})
	if _, err := h.AddEval(&id, nil, "suite", 0.6, 100, ""); err != nil {
		t.Fatal(err)
	}
	if err := asAgent(h).PromoteAutonomy(id, "auto", "suite", 0.5); !errors.Is(err, ErrAgentCannotSelfPromote) {
		t.Fatalf("promotion at a lowered threshold = %v, want ErrAgentCannotSelfPromote", err)
	}
	if err := h.PromoteAutonomy(id, "auto", "suite", 0.5); err != nil {
		t.Fatalf("a person may set the bar: %v", err)
	}
}
