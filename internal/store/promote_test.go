package store

import (
	"errors"
	"sync"
	"testing"
)

func TestPromoteNoteToDecisionAndMemory(t *testing.T) {
	s := humanStore(t)
	n1, _ := s.AddNote(nil, "use postgres", "manual")
	did, err := s.PromoteNote(n1, "decision", PromoteOpts{Rationale: "durability"})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s.ListDecisions("", nil)
	if len(d) != 1 || d[0].ID != did || d[0].Title != "use postgres" {
		t.Fatalf("decision = %+v", d)
	}
	if n, _ := s.GetNote(n1); !n.PromotedTo.Valid || n.PromotedTo.String != "decision" || n.PromotedID.Int64 != did {
		t.Errorf("note not marked: %+v", n)
	}
	n2, _ := s.AddNote(nil, "redis needs a ttl", "manual")
	if _, err := s.PromoteNote(n2, "memory", PromoteOpts{MemoryKind: "pitfall"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PromoteNote(n2, "memory", PromoteOpts{}); !errors.Is(err, ErrNoteAlreadyPromoted) {
		t.Fatalf("re-promote = %v", err)
	}
	if _, err := s.PromoteNote(n2, "nonsense", PromoteOpts{}); err == nil {
		t.Error("invalid kind accepted")
	}
}

func TestConcurrentPromotionsCreateExactlyOneRow(t *testing.T) {
	s := humanStore(t)
	n, _ := s.AddNote(nil, "race me", "manual")
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.PromoteNote(n, "memory", PromoteOpts{}); results <- err }()
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrNoteAlreadyPromoted) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d promotions succeeded, want exactly 1", ok)
	}
	if c := count(t, s, `SELECT COUNT(*) FROM memory`); c != 1 {
		t.Fatalf("memory rows = %d, want 1", c)
	}
}

func TestFailedPromotionReleasesTheNote(t *testing.T) {
	s := humanStore(t)
	n, _ := s.AddNote(nil, "x", "manual")
	if _, err := s.PromoteNote(n, "memory", PromoteOpts{MemoryKind: "bogus-kind"}); err == nil {
		t.Fatal("expected invalid memory kind to fail")
	}
	if got, _ := s.GetNote(n); got.PromotedTo.Valid {
		t.Fatal("a failed promotion left the note claimed")
	}
	if _, err := s.PromoteNote(n, "memory", PromoteOpts{}); err != nil {
		t.Fatalf("note should be promotable after a failed attempt: %v", err)
	}
}
