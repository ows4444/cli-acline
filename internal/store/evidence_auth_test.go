package store

import (
	"errors"
	"testing"
)

// `human_review` means a person looked at the work, and `verified` means a person
// confirmed a package is real. If the agent whose work is being judged could
// record either, the gate would be satisfied by an assertion.

func TestAgentCannotRecordAHumanReviewCheck(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	a := asAgent(h)

	if _, err := a.AddCheck(id, "human_review", "pass", ""); !errors.Is(err, ErrAgentCannotRecordHumanReview) {
		t.Fatalf("agent human_review = %v, want ErrAgentCannotRecordHumanReview", err)
	}
	if checks, _ := h.ListChecks(id); len(checks) != 0 {
		t.Fatalf("a refused check was recorded: %+v", checks)
	}
	if g, _ := h.EvaluateGate(id); g.OK() {
		t.Fatal("gate satisfied by an agent's human_review")
	}
	for _, kind := range []string{"test", "lint", "sast", "sca", "eval"} {
		if _, err := a.AddCheck(id, kind, "pass", ""); err != nil {
			t.Errorf("agent recording %s = %v, want allowed", kind, err)
		}
	}
	// A recorded *failure* is always welcome: it can only make the gate stricter.
	if _, err := a.AddCheck(id, "human_review", "fail", "not convinced"); err != nil {
		t.Errorf("agent recording a failing human_review = %v, want allowed", err)
	}
	if _, err := h.AddCheck(id, "human_review", "pass", ""); err != nil {
		t.Errorf("human human_review = %v", err)
	}
}

func TestHumanReviewNeedsTheTokenWhenEnabled(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.AddCheck(id, "human_review", "pass", ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("human without token = %v, want ErrApprovalTokenRequired", err)
	}
	if _, err := h.AddCheckWithRoleAndToken(id, nil, "human_review", "pass", "", "wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token = %v", err)
	}
	if _, err := asAgent(h).AddCheckWithRoleAndToken(id, nil, "human_review", "pass", "", tok); err != nil {
		t.Fatalf("agent with token = %v", err)
	}
	// other kinds never need it
	if _, err := asAgent(h).AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatalf("test check with a token enabled = %v", err)
	}
}

func TestAgentCannotMarkADependencyVerified(t *testing.T) {
	h := humanStore(t)
	a := asAgent(h)

	if _, err := a.AddDependency(nil, nil, "go", "github.com/x/y", "1.0.0", true); !errors.Is(err, ErrAgentCannotVerifyDependency) {
		t.Fatalf("agent adding a pre-verified dependency = %v, want ErrAgentCannotVerifyDependency", err)
	}
	id, err := a.AddDependency(nil, nil, "go", "github.com/x/y", "1.0.0", false)
	if err != nil {
		t.Fatalf("agent adding an unverified dependency = %v", err)
	}
	if err := a.VerifyDependency(id); !errors.Is(err, ErrAgentCannotVerifyDependency) {
		t.Fatalf("agent verifying = %v, want ErrAgentCannotVerifyDependency", err)
	}
	if deps, _ := h.ListDependencies(nil, true); len(deps) != 1 {
		t.Fatalf("dependency should still be unverified: %+v", deps)
	}
	if err := h.VerifyDependency(id); err != nil {
		t.Fatalf("human verifying = %v", err)
	}
	if deps, _ := h.ListDependencies(nil, true); len(deps) != 0 {
		t.Fatalf("still unverified after a person verified it: %+v", deps)
	}
}

func TestDependencyVerificationNeedsTheTokenWhenEnabled(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddDependency(nil, nil, "npm", "left-pad", "1.3.0", false)
	tok, _ := h.EnableApprovalToken()
	if err := h.VerifyDependency(id); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("human without token = %v", err)
	}
	if err := asAgent(h).VerifyDependencyWithToken(id, tok); err != nil {
		t.Fatalf("agent with token = %v", err)
	}
	if _, err := asAgent(h).AddDependencyWithToken(nil, nil, "npm", "x", "1", true, tok); err != nil {
		t.Fatalf("agent with token adding verified = %v", err)
	}
}
