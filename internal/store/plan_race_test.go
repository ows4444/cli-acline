package store

import (
	"path/filepath"
	"sync"
	"testing"
)

// openHandles opens n stores on one file, as n acline processes would: each has
// its own connection, so only SQLite's locking orders their writes.
func openHandles(t *testing.T, n int) (string, []*Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "race.db")
	var stores []*Store
	for i := 0; i < n; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		s.Actor = Actor{Type: "human", ID: "tester"}
		t.Cleanup(func() { s.Close() })
		stores = append(stores, s)
	}
	return path, stores
}

// race runs fn once per store at the same moment and returns how many succeeded.
func race(stores []*Store, fn func(s *Store) error) int {
	var wg sync.WaitGroup
	var mu sync.Mutex
	start := make(chan struct{})
	ok := 0
	for _, s := range stores {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			<-start
			if fn(s) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}(s)
	}
	close(start)
	wg.Wait()
	return ok
}

// The draft check ran before the transaction, so two approvals at once could
// both pass it and each create the plan's tasks.
func TestApprovingAPlanTwiceAtOnceCreatesTheTasksOnce(t *testing.T) {
	for round := 0; round < 5; round++ {
		_, stores := openHandles(t, 6)
		spec := approvedSpec(t, stores[0])
		id, err := stores[0].ProposePlan(spec, samplePlan())
		if err != nil {
			t.Fatal(err)
		}
		if n := race(stores, func(s *Store) error { _, err := s.ApprovePlan(id, ""); return err }); n != 1 {
			t.Fatalf("round %d: %d approvals succeeded, want 1", round, n)
		}
		if n := countRows(t, stores[0], `SELECT COUNT(*) FROM tasks`); n != len(samplePlan().Items) {
			t.Fatalf("round %d: %d tasks, want %d", round, n, len(samplePlan().Items))
		}
	}
}

// One draft per spec held only when proposals came one at a time.
func TestProposingTwiceAtOnceLeavesOneDraft(t *testing.T) {
	for round := 0; round < 5; round++ {
		_, stores := openHandles(t, 6)
		spec := approvedSpec(t, stores[0])
		if n := race(stores, func(s *Store) error { _, err := s.ProposePlan(spec, samplePlan()); return err }); n != 1 {
			t.Fatalf("round %d: %d proposals succeeded, want 1", round, n)
		}
		if n := countRows(t, stores[0], `SELECT COUNT(*) FROM plans WHERE status = 'draft'`); n != 1 {
			t.Fatalf("round %d: %d drafts", round, n)
		}
	}
}

// A revision and an approval at once: exactly one wins, and an approved plan
// is never also superseded.
func TestReviseAndApproveAtOnceDoNotBothApply(t *testing.T) {
	for round := 0; round < 5; round++ {
		_, stores := openHandles(t, 6)
		spec := approvedSpec(t, stores[0])
		id, err := stores[0].ProposePlan(spec, samplePlan())
		if err != nil {
			t.Fatal(err)
		}
		i := 0
		var mu sync.Mutex
		n := race(stores, func(s *Store) error {
			mu.Lock()
			i++
			approve := i%2 == 0
			mu.Unlock()
			if approve {
				_, err := s.ApprovePlan(id, "")
				return err
			}
			_, err := s.RevisePlan(id, samplePlan())
			return err
		})
		if n != 1 {
			t.Fatalf("round %d: %d of approve/revise succeeded, want 1", round, n)
		}
		p, err := stores[0].GetPlan(id)
		if err != nil {
			t.Fatal(err)
		}
		tasks := countRows(t, stores[0], `SELECT COUNT(*) FROM tasks`)
		if (p.Status == "approved") != (tasks == len(samplePlan().Items)) || (p.Status != "approved" && tasks != 0) {
			t.Fatalf("round %d: plan %s with %d tasks", round, p.Status, tasks)
		}
	}
}
