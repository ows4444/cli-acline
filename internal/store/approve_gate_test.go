package store

import (
	"errors"
	"fmt"
	"testing"
)

// Approving a spec or accepting a decision turns a draft into something a
// task, plan or later work is derived from or checked against, the same
// category of decision plan approval and memory review already require a
// person for. These tests close a gap where setSpecStatus/setDecisionStatus
// were callable directly with no actor check at all.

func TestApproveSpecRefusesAnAgentUnlessTokenPresented(t *testing.T) {
	h := humanStore(t)
	id, err := h.AddSpec("do the thing", "body")
	if err != nil {
		t.Fatal(err)
	}
	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	if err := a.ApproveSpec(id, ""); !errors.Is(err, ErrAgentCannotApproveSpec) {
		t.Fatalf("agent approve, no token = %v", err)
	}
	if sp, _ := h.GetSpec(id); sp.Status != "draft" {
		t.Fatalf("status changed despite refusal: %+v", sp)
	}
	if err := h.ApproveSpec(id, ""); err != nil {
		t.Fatalf("human approve = %v", err)
	}
	if sp, _ := h.GetSpec(id); sp.Status != "approved" {
		t.Fatalf("status = %+v", sp)
	}
}

func TestApproveSpecWithTheApprovalTokenWorksForAnAgent(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddSpec("do the thing", "body")
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	if err := a.ApproveSpec(id, "wrong"); err == nil {
		t.Fatal("wrong token accepted")
	}
	if err := a.ApproveSpec(id, tok); err != nil {
		t.Fatalf("correct token refused: %v", err)
	}
	if sp, _ := h.GetSpec(id); sp.Status != "approved" {
		t.Fatalf("status = %+v", sp)
	}
}

func TestAcceptDecisionRefusesAnAgentUnlessTokenPresented(t *testing.T) {
	h := humanStore(t)
	id, err := h.AddDecision("use postgres", DecisionOpts{Decision: "use postgres for durability"})
	if err != nil {
		t.Fatal(err)
	}
	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	if err := a.AcceptDecision(id, ""); !errors.Is(err, ErrAgentCannotAcceptDecision) {
		t.Fatalf("agent accept, no token = %v", err)
	}
	if d, _ := h.GetDecision(id); d.Status != "proposed" {
		t.Fatalf("status changed despite refusal: %+v", d)
	}
	if err := h.AcceptDecision(id, ""); err != nil {
		t.Fatalf("human accept = %v", err)
	}
	if d, _ := h.GetDecision(id); d.Status != "accepted" {
		t.Fatalf("status = %+v", d)
	}
}

func TestAcceptDecisionWithTheApprovalTokenWorksForAnAgent(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddDecision("use postgres", DecisionOpts{Decision: "use postgres for durability"})
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	a := *h
	a.Actor = Actor{Type: "agent", ID: "planner"}
	if err := a.AcceptDecision(id, tok); err != nil {
		t.Fatalf("correct token refused: %v", err)
	}
	if d, _ := h.GetDecision(id); d.Status != "accepted" {
		t.Fatalf("status = %+v", d)
	}
}

// Rejecting stays open to everyone, deliberately: it only makes the gate
// stricter, same philosophy as task rejection and plan rejection.
func TestDecisionRejectStaysOpenToAnAgent(t *testing.T) {
	a := agentStore(t)
	id, err := a.AddDecision("use mysql", DecisionOpts{Decision: "use mysql"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.setDecisionStatus(id, "rejected"); err != nil {
		t.Fatalf("agent reject = %v", err)
	}
}

// A decision, spec or memory status change used to be one statement and its
// audit event another (written by each adapter, after the fact). Now the store
// writes both in one transaction, exactly once.
func TestStatusChangesRecordExactlyOneEventEach(t *testing.T) {
	h := humanStore(t)
	count := func(eventType, msg string) int {
		t.Helper()
		events, err := h.QueryEvents(EventFilter{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range events {
			if e.Type == eventType && e.Message == msg {
				n++
			}
		}
		return n
	}
	d1, _ := h.AddDecision("a", DecisionOpts{})
	d2, _ := h.AddDecision("b", DecisionOpts{})
	d3, _ := h.AddDecision("c", DecisionOpts{})
	spec, _ := h.AddSpec("s", "body")
	mem, _ := h.AddMemory("store", "lesson", "a lesson", MemoryOpts{})

	for _, step := range []struct {
		run       func() error
		eventType string
		msg       string
	}{
		{func() error { return h.AcceptDecision(d1, "") }, "decision_recorded", fmt.Sprintf("decision #%d accepted", d1)},
		{func() error { return h.RejectDecision(d2, "") }, "decision_recorded", fmt.Sprintf("decision #%d rejected", d2)},
		{func() error { return h.SupersedeDecision(d1, d3, "") }, "decision_recorded", fmt.Sprintf("decision #%d superseded by #%d", d1, d3)},
		{func() error { return h.ApproveSpec(spec, "") }, "spec_recorded", fmt.Sprintf("spec #%d approved", spec)},
		{func() error { return h.ReviewMemory(mem, true, "") }, "memory_reviewed", fmt.Sprintf("memory #%d approved", mem)},
	} {
		if err := step.run(); err != nil {
			t.Fatalf("%s: %v", step.msg, err)
		}
		if n := count(step.eventType, step.msg); n != 1 {
			t.Errorf("%q recorded %d time(s), want 1", step.msg, n)
		}
	}
	// A missing row changes nothing and records nothing.
	if err := h.AcceptDecision(9999, ""); err == nil {
		t.Fatal("accepting a missing decision succeeded")
	}
	if n := count("decision_recorded", "decision #9999 accepted"); n != 0 {
		t.Fatalf("an event was recorded for a missing decision")
	}
}
