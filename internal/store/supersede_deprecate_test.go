package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAgentCannotSupersedeAnApprovedSpec(t *testing.T) {
	h := humanStore(t)
	old := approvedSpec(t, h)
	a := asAgent(h)
	repl, _ := a.AddSpec("v2", "v2")

	if err := a.SupersedeSpec(old, repl, ""); !errors.Is(err, ErrAgentCannotRetireSpec) {
		t.Fatalf("agent supersede = %v, want ErrAgentCannotRetireSpec", err)
	}
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SupersedeSpec(old, repl, tok); err != nil {
		t.Fatalf("agent with token = %v", err)
	}
	sp, _ := h.GetSpec(old)
	if sp.Status != "superseded" || !sp.SupersededBy.Valid || sp.SupersededBy.Int64 != repl {
		t.Fatalf("superseded spec = %+v", sp)
	}
}

func TestASupersededSpecIsFinal(t *testing.T) {
	h := humanStore(t)
	s := asAgent(h)
	old, _ := s.AddSpec("draft one", "a")
	repl, _ := s.AddSpec("draft two", "b")
	if err := s.SupersedeSpec(old, old, ""); err == nil {
		t.Fatal("a spec superseded itself")
	}
	if err := s.SupersedeSpec(old, 999, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("superseding with a missing spec = %v, want ErrNotFound", err)
	}
	if err := s.SupersedeSpec(old, repl, ""); err != nil {
		t.Fatalf("agent superseding a draft = %v", err)
	}
	if err := h.ApproveSpec(old, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("approving a superseded spec = %v, want ErrInvalidTransition", err)
	}
	if _, err := s.ReviseSpec(old, "new text"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("revising a superseded spec = %v, want ErrInvalidTransition", err)
	}
	if err := s.SupersedeSpec(old, repl, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("superseding again = %v, want ErrInvalidTransition", err)
	}
}

func TestOnlyAnAcceptedDecisionCanBeDeprecated(t *testing.T) {
	h := humanStore(t)
	proposed, _ := h.AddDecision("maybe", DecisionOpts{})
	if err := h.DeprecateDecision(proposed, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("deprecating a proposed decision = %v, want ErrInvalidTransition", err)
	}

	id := acceptedDecision(t, h, "use postgres")
	if err := asAgent(h).DeprecateDecision(id, ""); !errors.Is(err, ErrAgentCannotRetireDecision) {
		t.Fatalf("agent deprecate = %v, want ErrAgentCannotRetireDecision", err)
	}
	if err := h.DeprecateDecision(id, ""); err != nil {
		t.Fatalf("person deprecate = %v", err)
	}
	if got := decisionStatus(t, h, id); got != "deprecated" {
		t.Fatalf("status = %s, want deprecated", got)
	}
	if err := h.AcceptDecision(id, ""); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("accepting a deprecated decision = %v, want ErrInvalidTransition", err)
	}
}

func TestSpecImplementedIsEveryDerivedTaskDone(t *testing.T) {
	h := humanStore(t)
	sp := approvedSpec(t, h)
	implemented := func() bool {
		t.Helper()
		got, err := h.SpecImplemented(sp)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if implemented() {
		t.Fatal("a spec with no tasks is implemented")
	}
	a, _ := h.AddTask("a", "", "normal", TaskOpts{SpecID: &sp})
	b, _ := h.AddTask("b", "", "normal", TaskOpts{SpecID: &sp})
	done := func(id int64) {
		if _, err := h.DB.Exec(`UPDATE tasks SET status = 'done' WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	done(a)
	if implemented() {
		t.Fatal("implemented with one task open")
	}
	done(b)
	if !implemented() {
		t.Fatal("not implemented with every task done")
	}
	if _, err := h.SpecImplemented(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing spec = %v, want ErrNotFound", err)
	}
}

// Nothing ever set `implemented`, but a store that has one opens with it approved.
func TestMigration16TurnsImplementedSpecsIntoApproved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v15.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Actor = Actor{Type: "human", ID: "owner"}
	id, _ := s.AddSpec("old", "body")
	for _, q := range []string{`UPDATE specs SET status = 'implemented' WHERE id = 1`, `PRAGMA user_version = 15`} {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sp, err := s.GetSpec(id)
	if err != nil || sp.Status != "approved" {
		t.Fatalf("migrated spec = %+v, %v", sp, err)
	}
}
