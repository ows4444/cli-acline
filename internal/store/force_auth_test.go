package store

import (
	"errors"
	"testing"
)

// `task done --force` overrides the completion gate. Until now it demanded the
// token only when one was enabled, so an agent with no token could force-complete
// any task; SOUL.md lists it as something the agent must never do.

func TestAgentCannotForceCompleteWithoutTheToken(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("risky", "", "normal", TaskOpts{Risk: "high"})
	a := asAgent(h)

	if _, err := a.CompleteTask(id, true, ""); !errors.Is(err, ErrAgentCannotOverrideGate) {
		t.Fatalf("agent force = %v, want ErrAgentCannotOverrideGate", err)
	}
	if task, _ := h.GetTask(id); task.Status == "done" {
		t.Fatal("the task was completed despite the refusal")
	}
	if approvals, _ := h.ListApprovals(id); len(approvals) != 0 {
		t.Fatalf("a refused override left approval rows: %+v", approvals)
	}
	// a person may override, and it is recorded
	res, err := h.CompleteTask(id, true, "")
	if err != nil || !res.Overridden {
		t.Fatalf("human force = %+v, %v", res, err)
	}
}

func TestForceWithTheTokenStillWorksForAnAgent(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("risky", "", "normal", TaskOpts{Risk: "high"})
	tok, _ := h.EnableApprovalToken()
	a := asAgent(h)
	if _, err := a.CompleteTask(id, true, "wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token = %v", err)
	}
	if res, err := a.CompleteTask(id, true, tok); err != nil || !res.Overridden {
		t.Fatalf("agent holding the token = %+v, %v", res, err)
	}
}

func TestAnAgentMayStillCompleteATaskWhoseGateIsSatisfiedWithoutForce(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("easy", "", "normal", TaskOpts{})
	a := asAgent(h)
	if _, err := a.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	if res, err := a.CompleteTask(id, false, ""); err != nil || res.Overridden {
		t.Fatalf("a satisfied gate needs no override: %+v, %v", res, err)
	}
	// and --force on an already-satisfied gate is not an override, so it needs nothing special
	id2, _ := h.AddTask("easy2", "", "normal", TaskOpts{})
	a.AddCheck(id2, "test", "pass", "")
	if res, err := a.CompleteTask(id2, true, ""); err != nil || res.Overridden {
		t.Fatalf("force on a satisfied gate = %+v, %v", res, err)
	}
}
