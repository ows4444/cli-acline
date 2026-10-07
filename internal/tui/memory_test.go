package tui

import (
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

func memorySession(t *testing.T, st *store.Store) *tuitest.Session {
	t.Helper()
	m, err := newShell(st, nil, noHash, memoryScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 140, 40)
	t.Cleanup(s.Close)
	return s
}

func TestMemoryLifecycleFromTheScreen(t *testing.T) {
	st := openTestStore(t)
	st.AddMemory("cli", "pitfall", "cobra reads flags before env", store.MemoryOpts{ForcePending: true})
	s := memorySession(t, st)
	shows(t, s, "Memory: pending (1)", "#1 [pitfall] pending", "cobra reads flags before env", "[a] approve", "[X] reject")

	s.Keys("a", "y")
	shows(t, s, "memory #1 approved", "Memory: pending (0)", "nothing here")
	s.Keys("f")
	shows(t, s, "Memory: approved (1)", "[F] forget", "[t] reconfirm")
	s.Keys("F", "y")
	shows(t, s, "memory #1 forgotten", "Memory: approved (0)")
	s.Keys("f", "f") // decaying, stale
	shows(t, s, "Memory: stale (1)", "[R] restore")
	s.Keys("R", "y")
	shows(t, s, "memory #1 restored", "Memory: stale (0)")
	if m, _ := st.ListMemory(store.MemoryFilter{Status: "approved"}); len(m) != 1 || m[0].Stale {
		t.Fatalf("memory = %+v, want one live approved entry", m)
	}
}

func TestDecayingMemoryCanBeReconfirmed(t *testing.T) {
	st := openTestStore(t)
	id, _ := st.AddMemory("store", "constraint", "one connection per store")
	if _, err := st.DB.Exec(`UPDATE memory SET reviewed_at = '2020-01-01T00:00:00Z', created_at = '2020-01-01T00:00:00Z' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	s := memorySession(t, st)
	s.Keys("f", "f") // approved, decaying
	shows(t, s, "Memory: decaying (1)", "approved, decaying", "not reconfirmed in 90 days")
	s.Keys("t", "y")
	shows(t, s, "memory #1 reconfirmed", "Memory: decaying (0)")
}

func TestPromoteANoteWithAForm(t *testing.T) {
	st := openTestStore(t)
	st.AddNote(nil, "retries hide flaky tests", "conversation")
	st.AddNote(nil, "use fts5 for search\nit is already embedded", "conversation")
	s := memorySession(t, st)
	s.Keys("n")
	shows(t, s, "Notes not yet promoted (2)", "note #1", "[enter] promote")

	s.Keys("enter")
	s.Keys("enter") // into memory
	shows(t, s, "Memory from note #1", "Kind", "Area")
	s.Keys("tab", "ci q", "enter") // q is typed, not quit
	shows(t, s, "note #1 promoted to memory #1", "Notes not yet promoted (1)")
	if m, _ := st.ListMemory(store.MemoryFilter{}); len(m) != 1 || m[0].Area.String != "ci q" || m[0].Kind != "lesson" {
		t.Fatalf("memory = %+v", m)
	}

	s.Keys("enter")
	s.Keys("down", "enter") // into a decision; the title starts as the note's first line
	shows(t, s, "Decision from note #2", "use fts5 for search")
	s.Keys("enter")
	shows(t, s, "note #2 promoted to decision #1 (proposed)", "Notes not yet promoted (0)")
	if d, _ := st.ListDecisions("proposed", nil); len(d) != 1 || d[0].Title != "use fts5 for search" {
		t.Fatalf("decisions = %+v", d)
	}
}
