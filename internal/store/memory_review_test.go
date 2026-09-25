package store

import (
	"errors"
	"testing"
)

func TestReviewMemoryApprovesRejectsAndAudits(t *testing.T) {
	s := humanStore(t)
	a, _ := s.AddMemory("cli", "lesson", "keep this")
	b, _ := s.AddMemory("cli", "lesson", "drop this")
	if err := s.ReviewMemory(a, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReviewMemory(b, false, ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListMemory(MemoryFilter{})
	status := map[int64]string{}
	for _, m := range got {
		status[m.ID] = m.Status
	}
	if status[a] != "approved" || status[b] != "rejected" {
		t.Errorf("statuses = %v", status)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events WHERE type='memory_reviewed'`); n != 2 {
		t.Errorf("memory_reviewed events = %d, want 2", n)
	}
}

func TestReviewMemoryRefusesAgents(t *testing.T) {
	s := newTestStore(t, Actor{Type: "agent", ID: "claude-code"})
	id, _ := s.AddMemory("cli", "lesson", "agent-written")
	if err := s.ReviewMemory(id, true, ""); !errors.Is(err, ErrAgentCannotReview) {
		t.Fatalf("agent review = %v, want ErrAgentCannotReview", err)
	}
	got, _ := s.ListMemory(MemoryFilter{})
	if got[0].Status != "pending" {
		t.Errorf("agent changed status to %q", got[0].Status)
	}
}
