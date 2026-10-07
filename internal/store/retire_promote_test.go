package store

import (
	"errors"
	"strconv"
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

// The eval a promotion cites was the latest of that suite anywhere in the
// shared store, so a measurement of one project's work promoted another's task.
// It now has to be the latest of that suite in the task's own project.
func TestPromotionCitesAnEvalFromTheTasksOwnProject(t *testing.T) {
	h := humanStore(t)
	a, _ := h.AddProject("a", "", "")
	b, _ := h.AddProject("b", "", "")
	inA, _ := h.AddTask("in a", "", "normal", TaskOpts{Autonomy: "hitl", ProjectID: &a})
	inB, _ := h.AddTask("in b", "", "normal", TaskOpts{Autonomy: "hitl", ProjectID: &b})
	if _, err := h.AddEval(&inA, &a, "suite", 0.99, 100, "measured on a"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Store{asAgent(h), h} {
		if err := s.PromoteAutonomy(inB, "auto", "suite", 0.9); err == nil {
			t.Fatalf("%s: project a's eval promoted a task in project b", s.Actor.Type)
		}
	}
	if task, _ := h.GetTask(inB); task.Autonomy != "hitl" {
		t.Fatalf("autonomy = %s", task.Autonomy)
	}
	// A low score in b is b's latest, even though a's is newer.
	if _, err := h.AddEval(nil, &b, "suite", 0.5, 100, "measured on b"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AddEval(&inA, &a, "suite", 1.0, 100, "a again"); err != nil {
		t.Fatal(err)
	}
	if err := asAgent(h).PromoteAutonomy(inB, "auto", "suite", 0.9); err == nil {
		t.Fatal("b's own low score was ignored in favour of a's")
	}
	if _, err := h.AddEval(nil, &b, "suite", 0.95, 100, "b improved"); err != nil {
		t.Fatal(err)
	}
	if err := asAgent(h).PromoteAutonomy(inB, "auto", "suite", 0.9); err != nil {
		t.Fatalf("b's own passing eval = %v", err)
	}
	if n := count(t, h, `SELECT COUNT(*) FROM events WHERE type = 'autonomy_promoted' AND task_id = `+strconv.FormatInt(inB, 10)); n != 1 {
		t.Errorf("autonomy_promoted events = %d, want 1", n)
	}
}
