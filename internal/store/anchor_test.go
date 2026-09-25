package store

import (
	"strings"
	"testing"
)

func TestAnchorDetectsTailTruncationThatTheChainAloneCannot(t *testing.T) {
	s := humanStore(t)
	for i := 0; i < 5; i++ {
		s.LogEvent(nil, nil, "note", "e")
	}
	head, ok, err := s.ChainHead()
	if err != nil || !ok || head.ID != 6 { // five notes after the hash_version marker
		t.Fatalf("head = %+v, %v, %v", head, ok, err)
	}
	anchor := head.String()
	if a, err := ParseAnchor(anchor); err != nil || s.CheckAnchor(a) != nil {
		t.Fatalf("fresh anchor should hold: %v", err)
	}
	s.LogEvent(nil, nil, "note", "later") // growth after the anchor is fine
	a, _ := ParseAnchor(anchor)
	if err := s.CheckAnchor(a); err != nil {
		t.Fatalf("anchor must survive later events: %v", err)
	}

	// The attack: drop the append-only trigger and delete the newest events.
	if _, err := s.DB.Exec(`DROP TRIGGER events_no_delete; DELETE FROM events WHERE id >= 5`); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.VerifyChain(); !r.OK() {
		t.Fatalf("premise: the truncated chain is still internally valid, got %+v", r)
	}
	if err := s.CheckAnchor(a); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncation not detected by the anchor: %v", err)
	}
}

func TestAnchorDetectsRewrittenHistory(t *testing.T) {
	s := humanStore(t)
	s.LogEvent(nil, nil, "note", "a")
	head, _, _ := s.ChainHead()
	if _, err := s.DB.Exec(`DROP TRIGGER events_no_update; UPDATE events SET hash = ? WHERE id = ?`, strings.Repeat("0", 64), head.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckAnchor(head); err == nil || !strings.Contains(err.Error(), "rewritten") {
		t.Fatalf("rewrite not detected: %v", err)
	}
}

func TestParseAnchorRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "5", "x:abc", "0:" + strings.Repeat("a", 64), "3:short"} {
		if _, err := ParseAnchor(bad); err == nil {
			t.Errorf("ParseAnchor(%q) accepted", bad)
		}
	}
}

func TestChainHeadOnEmptyStore(t *testing.T) {
	if _, ok, err := humanStore(t).ChainHead(); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
}
