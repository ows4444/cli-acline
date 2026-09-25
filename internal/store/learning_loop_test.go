package store

import (
	"strings"
	"testing"
)

func TestPromotedMemoryRecordsSourceNote(t *testing.T) {
	s := humanStore(t)
	n, _ := s.AddNote(nil, "redis needs a ttl", "manual")
	id, err := s.PromoteNote(n, "memory", PromoteOpts{MemoryKind: "pitfall"})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListMemory(MemoryFilter{})
	if len(all) != 1 || all[0].ID != id || all[0].SourceKind.String != "note" || all[0].SourceID.Int64 != n {
		t.Fatalf("provenance = %+v", all)
	}
}

func TestFailedCheckDraftsPendingFailurePattern(t *testing.T) {
	// A human recording the failure must still leave the draft pending.
	s := humanStore(t)
	tid, _ := s.AddTask("ship it", "", "normal", TaskOpts{Area: "store"})
	if _, err := s.AddCheck(tid, "test", "pass", "ok"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ListMemory(MemoryFilter{}); len(got) != 0 {
		t.Fatalf("passing check drafted memory: %+v", got)
	}
	for i := 0; i < 3; i++ { // retries must not flood the queue
		if _, err := s.AddCheck(tid, "test", "fail", "TestFoo panicked"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.ListMemory(MemoryFilter{})
	if len(got) != 1 {
		t.Fatalf("want 1 draft, got %+v", got)
	}
	m := got[0]
	if m.Kind != "failure_pattern" || m.Status != "pending" || m.Area.String != "store" ||
		m.SourceKind.String != "check:test" || m.SourceID.Int64 != tid {
		t.Errorf("draft = %+v", m)
	}
}

func TestRejectionDraftsFailurePattern(t *testing.T) {
	s := humanStore(t)
	tid, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.AddApproval(tid, "code_review", "", "rejected", "misses the edge case"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListMemory(MemoryFilter{Status: "pending"})
	if len(got) != 1 || got[0].SourceKind.String != "rejection" {
		t.Fatalf("draft = %+v", got)
	}
}

func TestRecallForTaskOnlyApprovedRelevantLessons(t *testing.T) {
	s := humanStore(t)
	tid, _ := s.AddTask("t", "", "normal", TaskOpts{Area: "store"})
	s.AddMemory("store", "pitfall", "store pitfall")        // approved, matches
	s.AddMemory("", "failure_pattern", "area-less pattern") // approved, matches
	s.AddMemory("cli", "pitfall", "other area")             // wrong area
	s.AddMemory("store", "lesson", "not a pitfall")         // wrong kind
	s.AddMemory("store", "pitfall", "pending", MemoryOpts{ForcePending: true})
	got, err := s.RecallForTask(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Body != "store pitfall" || got[1].Body != "area-less pattern" {
		t.Fatalf("recall = %+v", got)
	}
}

func TestCountDraftedMemoryIsSubsetOfPending(t *testing.T) {
	s := humanStore(t)
	tid, _ := s.AddTask("t", "", "normal", TaskOpts{})
	s.AddMemory("", "lesson", "hand-written pending", MemoryOpts{ForcePending: true})
	s.AddCheck(tid, "lint", "fail", "x")
	if p, _ := s.CountPendingMemory(); p != 2 {
		t.Errorf("pending = %d, want 2", p)
	}
	if d, _ := s.CountDraftedMemory(); d != 1 {
		t.Errorf("drafted = %d, want 1", d)
	}
}

func TestGateBlocksSkippedSecurityScanOnHighRiskOnly(t *testing.T) {
	s := humanStore(t)
	high, _ := s.AddTask("high", "", "normal", TaskOpts{Risk: "high"})
	s.AddCheck(high, "test", "pass", "")
	s.AddCheck(high, "sast", "skipped", "gosec is not installed")
	g, _ := s.EvaluateGate(high)
	found := false
	for _, b := range g.Blockers {
		found = found || strings.Contains(b, "sast check was skipped")
	}
	if !found {
		t.Fatalf("skipped sast not blocking: %+v", g)
	}
	// Re-running for real supersedes the skip.
	s.AddCheck(high, "sast", "pass", "")
	g, _ = s.EvaluateGate(high)
	for _, b := range g.Blockers {
		if strings.Contains(b, "skipped") {
			t.Fatalf("still blocked after a real pass: %+v", g)
		}
	}
	low, _ := s.AddTask("low", "", "normal", TaskOpts{Risk: "low"})
	s.AddCheck(low, "test", "pass", "")
	s.AddCheck(low, "sast", "skipped", "")
	if g, _ := s.EvaluateGate(low); !g.OK() {
		t.Fatalf("low-risk task blocked by skipped sast: %+v", g)
	}
}

func TestSimilarMemoryExactMatchWithoutEmbedder(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddMemory("cli", "lesson", "Redis needs a   TTL")
	if m, _ := s.SimilarMemory("redis needs a ttl", nil); m == nil || m.ID != id {
		t.Fatalf("exact match (case/space-insensitive) missed: %+v", m)
	}
	if m, _ := s.SimilarMemory("something unrelated", nil); m != nil {
		t.Fatalf("false match: %+v", m)
	}
	rej, _ := s.AddMemory("cli", "lesson", "rejected idea")
	s.setMemoryStatus(rej, "rejected")
	if m, _ := s.SimilarMemory("rejected idea", nil); m != nil {
		t.Fatalf("matched a rejected entry: %+v", m)
	}
}

func TestSimilarMemorySemanticMatch(t *testing.T) {
	s := humanStore(t)
	s.Embedder = &fakeEmbedder{model: "fake"}
	id, _ := s.AddMemory("", "lesson", "postgres durability matters")
	m, _ := s.SimilarMemory("we chose postgres for durability", nil)
	if m == nil || m.ID != id {
		t.Fatalf("semantic match missed: %+v", m)
	}
	if m, _ := s.SimilarMemory("redis cache latency", nil); m != nil {
		t.Fatalf("unrelated matched: %+v", m)
	}
}

func TestRecallAddsSemanticallyRelevantLessonsFromOtherAreas(t *testing.T) {
	s := humanStore(t)
	s.Embedder = &fakeEmbedder{model: "fake"}
	tid, _ := s.AddTask("add redis cache", "watch latency", "normal", TaskOpts{Area: "store"})
	s.AddMemory("cli", "pitfall", "redis cache entries need a ttl for latency") // other area, relevant
	s.AddMemory("cli", "pitfall", "postgres durability needs fsync")            // other area, unrelated
	s.AddMemory("cli", "lesson", "redis cache latency lesson (wrong kind)")     // not a pitfall
	s.AddMemory("cli", "pitfall", "redis cache pending", MemoryOpts{ForcePending: true})
	got, err := s.RecallForTask(tid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "redis cache entries need a ttl for latency" {
		t.Fatalf("recall = %+v", got)
	}
}
