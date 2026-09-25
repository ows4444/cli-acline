package store

import (
	"strings"
	"testing"
)

// Approved-spec text feeds context export, briefs and orchestrator prompts as
// authoritative. Rewriting it must therefore withdraw the approval, keep what
// was approved, and leave a record.

func approvedSpecWithBody(t *testing.T, s *Store, body string) int64 {
	t.Helper()
	id, err := s.AddSpec("the spec", body)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ApproveSpec(id, ""); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestReviseSpecWithdrawsApproval(t *testing.T) {
	h := humanStore(t)
	id := approvedSpecWithBody(t, h, "original body")

	v, err := asAgent(h).ReviseSpec(id, "rewritten body")
	if err != nil || v != 2 {
		t.Fatalf("ReviseSpec = %d, %v", v, err)
	}
	sp, _ := h.GetSpec(id)
	if sp.Status != "draft" {
		t.Fatalf("status after revising an approved spec = %q, want draft", sp.Status)
	}
	if sp.Body.String != "rewritten body" || sp.Version != 2 {
		t.Fatalf("spec = %+v", sp)
	}
	approved, _ := h.ListSpecs("approved", nil)
	if len(approved) != 0 {
		t.Fatal("a revised spec must not be listed as approved")
	}
}

func TestReviseSpecKeepsWhatWasApproved(t *testing.T) {
	h := humanStore(t)
	id := approvedSpecWithBody(t, h, "original body")
	if _, err := h.ReviseSpec(id, "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ReviseSpec(id, "third"); err != nil {
		t.Fatal(err)
	}
	vs, err := h.ListSpecVersions(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 2 || vs[0].Version != 1 || vs[0].Body != "original body" || vs[1].Version != 2 || vs[1].Body != "second" {
		t.Fatalf("history = %+v", vs)
	}
	if vs[0].Status != "approved" || vs[1].Status != "draft" {
		t.Fatalf("history should record the status each version had: %+v", vs)
	}
}

func TestReviseSpecIsAudited(t *testing.T) {
	h := humanStore(t)
	id := approvedSpecWithBody(t, h, "original body")
	if _, err := asAgent(h).ReviseSpec(id, "rewritten"); err != nil {
		t.Fatal(err)
	}
	events, err := h.QueryEvents(EventFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == "spec_revised" && strings.Contains(e.Message, "v1 -> v2") && strings.Contains(e.Message, "approved -> draft") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no spec_revised event in %+v", events)
	}
	if res, err := h.VerifyChain(); err != nil || !res.OK() {
		t.Fatalf("chain: %+v, %v", res, err)
	}
}

func TestReviseDraftSpecStaysDraftAndMissingSpecFails(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddSpec("draft", "one")
	if _, err := h.ReviseSpec(id, "two"); err != nil {
		t.Fatal(err)
	}
	if sp, _ := h.GetSpec(id); sp.Status != "draft" {
		t.Fatalf("status = %q", sp.Status)
	}
	if _, err := h.ReviseSpec(9999, "x"); err == nil {
		t.Fatal("revising a missing spec succeeded")
	}
}

func TestReapprovingARevisedSpecStillNeedsAPerson(t *testing.T) {
	h := humanStore(t)
	id := approvedSpecWithBody(t, h, "original")
	a := asAgent(h)
	if _, err := a.ReviseSpec(id, "rewritten"); err != nil {
		t.Fatal(err)
	}
	if err := a.ApproveSpec(id, ""); err == nil {
		t.Fatal("an agent re-approved its own rewrite")
	}
}

func TestMigrationAddsSpecVersionsToAStoreAtVersion10(t *testing.T) {
	path := t.TempDir() + "/old.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`DROP TABLE spec_versions; PRAGMA user_version = 10`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopening a version-10 store: %v", err)
	}
	defer s.Close()
	s.Actor = Actor{Type: "human", ID: "tester"}
	id, _ := s.AddSpec("s", "one")
	if _, err := s.ReviseSpec(id, "two"); err != nil {
		t.Fatalf("revise after migrating: %v", err)
	}
	if vs, _ := s.ListSpecVersions(id); len(vs) != 1 {
		t.Fatalf("history after migrating = %+v", vs)
	}
}
