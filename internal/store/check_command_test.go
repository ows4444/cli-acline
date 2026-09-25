package store

import (
	"errors"
	"testing"
)

// A runner result is evidence because the command that produced it was not the
// caller's choice: it is the project's runner, set by a person, or the built-in
// default. A command the caller names itself (`check run --cmd true`) proves
// only that the caller could find a command that exits 0.

func adHocRunnerCheck(s *Store, task int64, token string) (int64, error) {
	return s.AddCheckWithMeta(task, nil, "test", "pass", "ran: true", token,
		CheckMeta{Source: CheckSourceRunner, TreeHash: treeA, AdHocCommand: true})
}

func TestAgentCannotRecordARunnerPassFromACommandItChose(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	a := asAgent(h)

	if _, err := adHocRunnerCheck(a, id, ""); !errors.Is(err, ErrAgentCannotChooseCheckCommand) {
		t.Fatalf("agent ad-hoc runner check = %v, want ErrAgentCannotChooseCheckCommand", err)
	}
	if err := a.AuthorizeAdHocCheckCommand(""); !errors.Is(err, ErrAgentCannotChooseCheckCommand) {
		t.Fatalf("AuthorizeAdHocCheckCommand(agent) = %v", err)
	}
	if checks, _ := h.ListChecks(id); len(checks) != 0 {
		t.Fatalf("a refused check was written: %+v", checks)
	}

	// The default or the project's runner is not ad hoc, so agents may still run it.
	runnerCheck(t, a, id, "test", "pass", treeA)
}

func TestAPersonMayChooseACheckCommand(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	if _, err := adHocRunnerCheck(h, id, ""); err != nil {
		t.Fatalf("person ad-hoc runner check = %v", err)
	}
}

func TestAdHocCheckCommandNeedsTheTokenOnceEnabled(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adHocRunnerCheck(h, id, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("no token = %v, want ErrApprovalTokenRequired", err)
	}
	if _, err := adHocRunnerCheck(h, id, "acl_wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token = %v, want ErrApprovalTokenInvalid", err)
	}
	if _, err := adHocRunnerCheck(asAgent(h), id, tok); err != nil {
		t.Fatalf("agent with the token = %v", err)
	}
}
