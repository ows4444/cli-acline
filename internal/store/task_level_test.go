package store

import (
	"errors"
	"strings"
	"testing"
)

// Risk and autonomy decide whether a task needs a person's approval to
// complete. Loosening either is therefore a privileged change, and every change
// is recorded.

func asAgent(s *Store) *Store {
	a := *s
	a.Actor = Actor{Type: "agent", ID: "planner"}
	return &a
}

func TestAgentCannotLowerRisk(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("risky", "", "normal", TaskOpts{Risk: "critical", Autonomy: "hitl"})
	a := asAgent(h)

	if err := a.UpdateTaskRisk(id, "low"); !errors.Is(err, ErrAgentCannotLoosenTask) {
		t.Fatalf("agent lowering risk = %v, want ErrAgentCannotLoosenTask", err)
	}
	if err := a.UpdateTaskRisk(id, "high"); !errors.Is(err, ErrAgentCannotLoosenTask) {
		t.Fatalf("agent lowering critical -> high = %v, want ErrAgentCannotLoosenTask", err)
	}
	if task, _ := h.GetTask(id); task.Risk != "critical" {
		t.Fatalf("risk changed despite refusal: %s", task.Risk)
	}
	if err := h.UpdateTaskRisk(id, "low"); err != nil {
		t.Fatalf("human lowering risk = %v", err)
	}
}

func TestAgentMayRaiseRiskAndTightenAutonomy(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "low", Autonomy: "hotl"})
	a := asAgent(h)
	if err := a.UpdateTaskRisk(id, "high"); err != nil {
		t.Fatalf("raising risk = %v", err)
	}
	if err := a.UpdateTaskAutonomy(id, "hitl"); err != nil {
		t.Fatalf("tightening autonomy = %v", err)
	}
	if err := a.UpdateTaskRisk(id, "high"); err != nil {
		t.Fatalf("setting the same risk again = %v", err)
	}
}

func TestAgentCannotLoosenAutonomy(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high", Autonomy: "hitl"})
	a := asAgent(h)
	for _, to := range []string{"hotl", "auto"} {
		if err := a.UpdateTaskAutonomy(id, to); !errors.Is(err, ErrAgentCannotLoosenTask) {
			t.Fatalf("agent hitl -> %s = %v, want ErrAgentCannotLoosenTask", to, err)
		}
	}
	if task, _ := h.GetTask(id); task.Autonomy != "hitl" {
		t.Fatalf("autonomy changed despite refusal: %s", task.Autonomy)
	}
	if err := h.UpdateTaskAutonomy(id, "hotl"); err != nil {
		t.Fatalf("human loosening = %v", err)
	}
}

func TestAgentCannotCreateAnAutoTask(t *testing.T) {
	h := humanStore(t)
	a := asAgent(h)
	if _, err := a.AddTask("t", "", "normal", TaskOpts{Autonomy: "auto"}); !errors.Is(err, ErrAgentCannotLoosenTask) {
		t.Fatalf("agent creating an auto task = %v, want ErrAgentCannotLoosenTask", err)
	}
	if _, err := a.AddTask("t", "", "normal", TaskOpts{Autonomy: "hitl", Risk: "high"}); err != nil {
		t.Fatalf("agent creating a supervised task = %v", err)
	}
	if _, err := h.AddTask("t", "", "normal", TaskOpts{Autonomy: "auto"}); err != nil {
		t.Fatalf("human creating an auto task = %v", err)
	}
}

func TestLoosenedTaskNeedsTheTokenWhenEnabled(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high", Autonomy: "hitl"})
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.UpdateTaskRisk(id, "low"); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("human without token = %v, want ErrApprovalTokenRequired", err)
	}
	a := asAgent(h)
	if err := a.UpdateTaskRiskWithToken(id, "low", "wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token = %v, want ErrApprovalTokenInvalid", err)
	}
	if err := a.UpdateTaskRiskWithToken(id, "low", tok); err != nil {
		t.Fatalf("agent holding the token = %v", err)
	}
	if err := a.UpdateTaskAutonomyWithToken(id, "auto", tok); err != nil {
		t.Fatalf("agent holding the token, autonomy = %v", err)
	}
}

func TestRiskAndAutonomyChangesAreAudited(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high", Autonomy: "hitl"})
	if err := h.UpdateTaskRisk(id, "medium"); err != nil {
		t.Fatal(err)
	}
	if err := h.UpdateTaskAutonomy(id, "hotl"); err != nil {
		t.Fatal(err)
	}
	events, err := h.ListEvents(&id, 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		if e.Type == "risk_changed" || e.Type == "autonomy_changed" {
			got = append(got, e.Type+": "+e.Message)
		}
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{"risk_changed: risk high -> medium", "autonomy_changed: autonomy hitl -> hotl"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing event %q in:\n%s", want, joined)
		}
	}
	if res, err := h.VerifyChain(); err != nil || !res.OK() {
		t.Fatalf("chain after audited changes: %+v, %v", res, err)
	}
}

func TestPromoteAutonomyStillWorksForAnAgentWithAMeasuredEval(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Autonomy: "hitl"})
	if _, err := h.AddEval(&id, nil, "suite", 0.99, 100, ""); err != nil {
		t.Fatal(err)
	}
	a := asAgent(h)
	if err := a.PromoteAutonomy(id, "auto", "suite", 0.9); err != nil {
		t.Fatalf("eval-backed promotion = %v", err)
	}
	if task, _ := h.GetTask(id); task.Autonomy != "auto" {
		t.Fatalf("autonomy = %s", task.Autonomy)
	}
}
