package store

import (
	"errors"
	"testing"
)

// An agent could mark an approved constraint stale (`memory forget`), dropping
// it from every later session's context, with no person involved and no audit
// event.

func approvedConstraint(t *testing.T, s *Store) int64 {
	t.Helper()
	actor := s.Actor
	s.Actor = Actor{Type: "human", ID: "owner"}
	id, err := s.AddMemory("cli", "constraint", "never change package a without a migration plan")
	s.Actor = actor
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func memoryRow(t *testing.T, s *Store, id int64) (stale bool, reviewedAt string) {
	t.Helper()
	var n int
	var r *string
	if err := s.DB.QueryRow(`SELECT stale, reviewed_at FROM memory WHERE id = ?`, id).Scan(&n, &r); err != nil {
		t.Fatal(err)
	}
	if r != nil {
		reviewedAt = *r
	}
	return n != 0, reviewedAt
}

func TestAgentCannotForgetRestoreOrReconfirmMemory(t *testing.T) {
	s := agentStore(t)
	id := approvedConstraint(t, s)
	_, before := memoryRow(t, s, id)
	events := count(t, s, `SELECT COUNT(*) FROM events`)

	if err := s.SetMemoryStale(id, true); !errors.Is(err, ErrAgentCannotRetireMemory) {
		t.Fatalf("agent forget = %v, want ErrAgentCannotRetireMemory", err)
	}
	if err := s.SetMemoryStale(id, false); !errors.Is(err, ErrAgentCannotRetireMemory) {
		t.Fatalf("agent restore = %v, want ErrAgentCannotRetireMemory", err)
	}
	if err := s.TouchMemory(id); !errors.Is(err, ErrAgentCannotRetireMemory) {
		t.Fatalf("agent touch = %v, want ErrAgentCannotRetireMemory", err)
	}
	if stale, after := memoryRow(t, s, id); stale || after != before {
		t.Errorf("a refused agent changed the entry: stale=%v reviewed_at %q -> %q", stale, before, after)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events`); n != events {
		t.Errorf("a refused agent wrote %d event(s)", n-events)
	}
}

func TestPersonForgetsRestoresAndReconfirmsMemoryWithAnEvent(t *testing.T) {
	s := humanStore(t)
	id := approvedConstraint(t, s)
	if err := s.SetMemoryStale(id, true); err != nil {
		t.Fatal(err)
	}
	if stale, _ := memoryRow(t, s, id); !stale {
		t.Fatal("forget did not mark the entry stale")
	}
	if err := s.SetMemoryStale(id, false); err != nil {
		t.Fatal(err)
	}
	if stale, _ := memoryRow(t, s, id); stale {
		t.Fatal("restore did not clear stale")
	}
	if err := s.TouchMemory(id); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"memory_forgotten", "memory_restored", "memory_reconfirmed"} {
		if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type = '`+typ+`'`); n != 1 {
			t.Errorf("%s events = %d, want 1", typ, n)
		}
	}
	if err := s.SetMemoryStale(999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("forgetting a missing entry = %v, want ErrNotFound", err)
	}
	if c, err := s.VerifyChain(); err != nil || !c.OK() {
		t.Fatalf("chain after memory changes: %+v, %v", c, err)
	}
}

func TestRetiringMemoryNeedsTheTokenWhenEnabled(t *testing.T) {
	s := humanStore(t)
	id := approvedConstraint(t, s)
	token, err := s.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemoryStaleWithToken(id, true, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("no token = %v, want ErrApprovalTokenRequired", err)
	}
	if err := s.SetMemoryStaleWithToken(id, true, "acl_wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token = %v, want ErrApprovalTokenInvalid", err)
	}
	if stale, _ := memoryRow(t, s, id); stale {
		t.Fatal("a refused forget changed the entry")
	}
	// With the token even an agent may act: it is acting for the person who holds it.
	s.Actor = Actor{Type: "agent", ID: "claude-code"}
	if err := s.SetMemoryStaleWithToken(id, true, token); err != nil {
		t.Fatalf("valid token = %v", err)
	}
	if err := s.TouchMemoryWithToken(id, token); err != nil {
		t.Fatalf("touch with valid token = %v", err)
	}
}
