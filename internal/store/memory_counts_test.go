package store

import (
	"testing"
	"time"
)

// Project A's dashboard reported project B's pending reviews because the three
// counters took no project.
func TestMemoryCountersCanBeScopedToAProject(t *testing.T) {
	h := humanStore(t)
	a, _ := h.AddProject("a", "/tmp/a", "hotl")
	b, _ := h.AddProject("b", "/tmp/b", "hotl")
	ag := asAgent(h) // agent-written entries land pending

	ag.AddMemory("x", "lesson", "a lesson", MemoryOpts{ProjectID: &a})
	ag.AddMemory("x", "lesson", "b lesson one", MemoryOpts{ProjectID: &b})
	ag.AddMemory("x", "lesson", "b lesson two", MemoryOpts{ProjectID: &b})
	// a drafted entry (from a failing check) for project b
	tb, _ := h.AddTask("tb", "", "normal", TaskOpts{ProjectID: &b})
	h.AddCheck(tb, "test", "fail", "boom")

	for _, tc := range []struct {
		name        string
		project     *int64
		wantPending int
		wantDrafted int
	}{
		{"all projects", nil, 4, 1},
		{"project a", &a, 1, 0},
		{"project b", &b, 3, 1},
	} {
		if n, err := h.CountPendingMemoryIn(tc.project); err != nil || n != tc.wantPending {
			t.Errorf("%s: pending = %d, %v; want %d", tc.name, n, err, tc.wantPending)
		}
		if n, err := h.CountDraftedMemoryIn(tc.project); err != nil || n != tc.wantDrafted {
			t.Errorf("%s: drafted = %d, %v; want %d", tc.name, n, err, tc.wantDrafted)
		}
	}
	// the unscoped forms keep meaning "everything"
	if n, _ := h.CountPendingMemory(); n != 4 {
		t.Errorf("CountPendingMemory = %d, want 4", n)
	}
	if n, _ := h.CountDraftedMemory(); n != 1 {
		t.Errorf("CountDraftedMemory = %d, want 1", n)
	}
}

func TestDecayCounterCanBeScopedToAProject(t *testing.T) {
	h := humanStore(t)
	a, _ := h.AddProject("a", "/tmp/a", "hotl")
	b, _ := h.AddProject("b", "/tmp/b", "hotl")
	ida, _ := h.AddMemory("x", "lesson", "old a", MemoryOpts{ProjectID: &a})
	idb, _ := h.AddMemory("x", "lesson", "old b", MemoryOpts{ProjectID: &b})
	old := time.Now().AddDate(0, 0, -200)
	backdateMemory(t, h, ida, old, old)
	backdateMemory(t, h, idb, old, old)

	if n, _ := h.CountDecayCandidatesIn(90, nil); n != 2 {
		t.Errorf("all = %d, want 2", n)
	}
	if n, _ := h.CountDecayCandidatesIn(90, &a); n != 1 {
		t.Errorf("project a = %d, want 1", n)
	}
	if n, _ := h.CountDecayCandidates(90); n != 2 {
		t.Errorf("CountDecayCandidates = %d, want 2", n)
	}
}
