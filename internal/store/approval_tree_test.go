package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// An approval was not tied to the code it approved: after a person approved a
// high-risk task, the agent could change the code, re-run the checks against
// the new code, and complete it on the old approval. Approvals now record the
// tree they were given for, and the gate wants a new one when the code changed.

func approveTree(t *testing.T, s *Store, task int64, tree string) {
	t.Helper()
	if _, err := s.RecordApproval(ApprovalRequest{TaskID: task, Decision: "approved", By: "bob", TreeHash: tree}); err != nil {
		t.Fatal(err)
	}
}

func TestAnApprovalExpiresWhenTheCodeChanges(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	runnerCheck(t, h, id, "test", "pass", treeA)
	approveTree(t, h, id, treeA)
	if g, _ := h.EvaluateGateForTree(id, treeA); !g.OK() {
		t.Fatalf("approved on the same code: %+v", g)
	}

	// The code changes and the checks are re-run on it: the approval is stale.
	runnerCheck(t, h, id, "test", "pass", treeB)
	g, _ := h.EvaluateGateForTree(id, treeB)
	if g.OK() || !strings.Contains(strings.Join(g.Blockers, "\n"), "approval") {
		t.Fatalf("an approval of other code completed the task: %+v", g)
	}
	if _, err := h.CompleteTaskForTree(id, false, "", treeB); err == nil {
		t.Fatal("task done accepted an approval of older code")
	}

	approveTree(t, h, id, treeB)
	if g, _ := h.EvaluateGateForTree(id, treeB); !g.OK() {
		t.Fatalf("re-approved on the current code, still blocked: %+v", g)
	}
}

func TestAnApprovalWithNoTreeOnlyWarns(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Autonomy: "hitl"})
	runnerCheck(t, h, id, "test", "pass", treeA)
	approve(t, h, id) // recorded before approvals carried a tree
	g, _ := h.EvaluateGateForTree(id, treeA)
	if !g.OK() {
		t.Fatalf("an approval with no tree blocked: %+v", g)
	}
	if !strings.Contains(strings.Join(g.Warnings, "\n"), "not tied to a version of the code") {
		t.Errorf("no warning that the approval is not tied to the code: %+v", g.Warnings)
	}
}

func TestAnApprovalsTreeIsSealed(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	approve(t, h, id) // no tree: the original seal format
	approveTree(t, h, id, treeA)
	if res, err := h.VerifyRecords(); err != nil || !res.OK() || res.Checked != 2 {
		t.Fatalf("seals before tampering: %+v, %v", res, err)
	}
	if _, err := h.DB.Exec(`DROP TRIGGER approvals_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DB.Exec(`UPDATE approvals SET tree_hash = ? WHERE id = 2`, treeB); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.VerifyRecords(); res.OK() {
		t.Fatalf("swapping an approval's tree went unnoticed: %+v", res)
	}
}

func TestMigrationAddsApprovalTreeToAVersion13Store(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v13.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Actor = Actor{Type: "human", ID: "owner"}
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}
	// Turn it back into a version 13 store: no tree_hash column.
	for _, q := range []string{`ALTER TABLE approvals DROP COLUMN tree_hash`, `PRAGMA user_version = 13`} {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.DB.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != SchemaVersion() || v < 14 {
		t.Fatalf("user_version = %d", v)
	}
	approvals, err := s.ListApprovals(id)
	if err != nil || len(approvals) != 1 || approvals[0].TreeHash.Valid {
		t.Fatalf("migrated approvals = %+v, %v", approvals, err)
	}
	if res, err := s.VerifyRecords(); err != nil || !res.OK() {
		t.Fatalf("a pre-migration approval no longer verifies: %+v, %v", res, err)
	}
}
