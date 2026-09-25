package store

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// backdateMemory rewrites created_at/reviewed_at directly, since AddMemory
// always stamps "now" — decay is a function of elapsed time, so tests need
// to simulate an entry written/reconfirmed in the past.
func backdateMemory(t *testing.T, s *Store, id int64, createdAt, reviewedAt time.Time) {
	t.Helper()
	var reviewedArg any
	if !reviewedAt.IsZero() {
		reviewedArg = reviewedAt.UTC().Format(time.RFC3339)
	}
	if _, err := s.DB.Exec(`UPDATE memory SET created_at = ?, reviewed_at = ? WHERE id = ?`,
		createdAt.UTC().Format(time.RFC3339), reviewedArg, id); err != nil {
		t.Fatal(err)
	}
}

func TestDecayCandidatesFindsOnlyOldReconfirmedEntries(t *testing.T) {
	s := humanStore(t)
	now := time.Now()

	oldID, err := s.AddMemory("cli", "lesson", "stale-ish lesson")
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, oldID, now.AddDate(0, 0, -120), now.AddDate(0, 0, -120))

	recentID, err := s.AddMemory("cli", "lesson", "recent lesson")
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, recentID, now.AddDate(0, 0, -10), now.AddDate(0, 0, -10))

	candidates, err := s.DecayCandidates(90, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != oldID {
		t.Fatalf("expected only the 120-day-old entry (#%d), got %+v", oldID, candidates)
	}
}

func TestDecayCandidatesFallsBackToCreatedAtWhenNeverReviewed(t *testing.T) {
	s := agentStore(t) // agent-written entries land pending, with no reviewed_at
	id, err := s.AddMemory("cli", "lesson", "agent lesson, never reviewed")
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, id, time.Now().AddDate(0, 0, -200), time.Time{})
	// Still pending, not approved -- decay only tracks approved entries.
	candidates, err := s.DecayCandidates(90, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected a pending (not approved) entry to be excluded from decay, got %+v", candidates)
	}

	if err := s.setMemoryStatus(id, "approved"); err != nil {
		t.Fatal(err)
	}
	// setMemoryStatus just stamped a fresh reviewed_at, so undo that for
	// this test's premise (approved, but with no *meaningful* review since
	// created 200 days ago).
	backdateMemory(t, s, id, time.Now().AddDate(0, 0, -200), time.Time{})

	candidates, err = s.DecayCandidates(90, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != id {
		t.Fatalf("expected the old, never-reconfirmed entry to be a decay candidate, got %+v", candidates)
	}
}

func TestDecayCandidatesExcludesStaleEntries(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddMemory("cli", "lesson", "already forgotten")
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, id, time.Now().AddDate(0, 0, -365), time.Now().AddDate(0, 0, -365))
	if err := s.SetMemoryStale(id, true); err != nil {
		t.Fatal(err)
	}

	candidates, err := s.DecayCandidates(90, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected an already-stale entry to be excluded from decay, got %+v", candidates)
	}
}

func TestTouchMemoryResetsDecayClock(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddMemory("cli", "lesson", "aging lesson")
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, id, time.Now().AddDate(0, 0, -120), time.Now().AddDate(0, 0, -120))

	candidates, err := s.DecayCandidates(90, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected the entry to be a decay candidate before touching it, got %+v", candidates)
	}

	if err := s.TouchMemory(id); err != nil {
		t.Fatal(err)
	}

	candidates, err = s.DecayCandidates(90, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected TouchMemory to reset the decay clock, still got %+v", candidates)
	}
}

func TestCountDecayCandidatesMatchesDecayCandidates(t *testing.T) {
	s := humanStore(t)
	for i := 0; i < 3; i++ {
		id, err := s.AddMemory("cli", "lesson", "aging lesson")
		if err != nil {
			t.Fatal(err)
		}
		backdateMemory(t, s, id, time.Now().AddDate(0, 0, -100), time.Now().AddDate(0, 0, -100))
	}
	count, err := s.CountDecayCandidates(90)
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected count 3, got %d", count)
	}
}

func TestDecayCandidatesScopesToProject(t *testing.T) {
	s := humanStore(t)
	projA, err := s.AddProject("proj-a", "", "hotl")
	if err != nil {
		t.Fatal(err)
	}
	projB, err := s.AddProject("proj-b", "", "hotl")
	if err != nil {
		t.Fatal(err)
	}

	idA, err := s.AddMemory("cli", "lesson", "project A lesson", MemoryOpts{ProjectID: &projA})
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, idA, time.Now().AddDate(0, 0, -100), time.Now().AddDate(0, 0, -100))

	idB, err := s.AddMemory("cli", "lesson", "project B lesson", MemoryOpts{ProjectID: &projB})
	if err != nil {
		t.Fatal(err)
	}
	backdateMemory(t, s, idB, time.Now().AddDate(0, 0, -100), time.Now().AddDate(0, 0, -100))

	candidates, err := s.DecayCandidates(90, &projA)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].ID != idA {
		t.Fatalf("expected only project A's entry, got %+v", candidates)
	}
}

// The auto-drafted failure_pattern cut the failing check's detail at a byte
// count, which split a multibyte character and stored invalid UTF-8.
func TestDraftedMemoryFromAMultibyteFailureIsValidUTF8(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	detail := "a" + strings.Repeat("€", 200) // 601 bytes; the cut at 300 falls inside a €
	if _, err := h.AddCheck(id, "test", "fail", detail); err != nil {
		t.Fatal(err)
	}
	mem, err := h.ListMemory(MemoryFilter{Status: "pending"})
	if err != nil || len(mem) != 1 {
		t.Fatalf("drafted memory = %+v, %v", mem, err)
	}
	if !utf8.ValidString(mem[0].Body) {
		t.Fatalf("drafted memory body is not valid UTF-8: %q", mem[0].Body)
	}
	if !strings.HasSuffix(mem[0].Body, "...") {
		t.Errorf("a shortened detail should end with an ellipsis: %q", mem[0].Body)
	}
}

func TestSearchSnippetFromMultibyteTextIsValidUTF8(t *testing.T) {
	if got := truncateSnippet(strings.Repeat("日", 200), 220); !utf8.ValidString(got) {
		t.Fatalf("snippet %q is not valid UTF-8", got)
	}
}
