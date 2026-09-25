package store

import (
	"strings"
	"testing"
)

// A record with no seal used to be reported as "predates sealing", a benign
// category, so rows inserted straight into the database (or by a snapshot
// import) were invisible to `acline verify`. Once sealing has started, an
// unsealed record with a later id is tampering.

func rawApproval(t *testing.T, s *Store, id, taskID int64) {
	t.Helper()
	if _, err := s.DB.Exec(
		`INSERT INTO approvals (id, task_id, kind, approver, decision, note, actor_type, actor_id, created_at)
		 VALUES (?, ?, 'code_review', 'alice', 'approved', 'lgtm', 'human', 'alice', '2026-01-01T00:00:00Z')`, id, taskID); err != nil {
		t.Fatal(err)
	}
}

func rawCheck(t *testing.T, s *Store, id, taskID int64) {
	t.Helper()
	if _, err := s.DB.Exec(
		`INSERT INTO checks (id, task_id, kind, status, detail, actor_type, actor_id, created_at)
		 VALUES (?, ?, 'test', 'pass', '', 'agent', 'x', '2026-01-01T00:00:00Z')`, id, taskID); err != nil {
		t.Fatal(err)
	}
}

func watermarkEvents(t *testing.T, s *Store) []Event {
	t.Helper()
	all, err := s.QueryEvents(EventFilter{Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, e := range all {
		if e.Type == "seal_watermark" {
			out = append(out, e)
		}
	}
	return out
}

func TestUnsealedApprovalAfterSealingStartedIsTampering(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	if _, err := h.AddCheck(id, "test", "pass", ""); err != nil { // sealing starts here
		t.Fatal(err)
	}
	rawApproval(t, h, 9001, id)

	res, err := h.VerifyRecords()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() || res.BadRef != "approvals#9001" {
		t.Fatalf("forged approval not flagged: %+v", res)
	}
	if !strings.Contains(res.Reason, "no seal") {
		t.Errorf("reason should say the record was never sealed: %q", res.Reason)
	}
}

func TestUnsealedCheckAfterSealingStartedIsTampering(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	if _, err := h.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}
	rawCheck(t, h, 500, id)
	res, _ := h.VerifyRecords()
	if res.OK() || res.BadRef != "checks#500" {
		t.Fatalf("forged check not flagged: %+v", res)
	}
}

func TestRecordsFromBeforeSealingStayUnsealedNotTampering(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	// Rows written by an older acline that did not seal.
	rawApproval(t, h, 1, id)
	rawCheck(t, h, 1, id)

	if res, _ := h.VerifyRecords(); !res.OK() || res.Unsealed != 2 {
		t.Fatalf("before any sealed insert: %+v", res)
	}
	// The first sealed insert draws the line above the legacy rows.
	if _, err := h.AddCheck(id, "lint", "pass", ""); err != nil {
		t.Fatal(err)
	}
	res, err := h.VerifyRecords()
	if err != nil || !res.OK() {
		t.Fatalf("legacy rows must not be flagged once sealing starts: %+v, %v", res, err)
	}
	if res.Unsealed != 2 || res.Checked != 1 {
		t.Errorf("counts = %+v, want 2 unsealed legacy rows and 1 sealed", res)
	}
}

func TestSealWatermarkIsWrittenOnceAndInsideTheChain(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	for i := 0; i < 3; i++ {
		if _, err := h.AddCheck(id, "test", "pass", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}
	wm := watermarkEvents(t, h)
	if len(wm) != 1 {
		t.Fatalf("%d watermark events, want exactly 1", len(wm))
	}
	if wm[0].Hash.String == "" {
		t.Error("watermark event is not hash-chained")
	}
	if res, err := h.VerifyChain(); err != nil || !res.OK() {
		t.Fatalf("chain: %+v, %v", res, err)
	}
}

func TestSnapshotImportedApprovalIsFlaggedByVerify(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	if _, err := h.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	// A person imports a snapshot that carries an approval nobody sealed.
	if _, err := h.LoadSnapshot(strings.NewReader(forgedApprovalSnapshot("1"))); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.VerifyRecords(); res.OK() {
		t.Fatalf("an approval that arrived without its seal must be reported: %+v", res)
	}
}

func TestGenuineRecordsStillVerifyAfterTheWatermark(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	h.AddCheck(id, "test", "pass", "")
	h.AddApproval(id, "code_review", "bob", "approved", "ok")
	h.AddCheck(id, "lint", "fail", "x")
	res, err := h.VerifyRecords()
	if err != nil || !res.OK() || res.Checked != 3 || res.Unsealed != 0 {
		t.Fatalf("%+v, %v", res, err)
	}
}
