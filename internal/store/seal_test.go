package store

import "testing"

func sealedStore(t *testing.T) (*Store, int64) {
	t.Helper()
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	if _, err := s.AddCheck(id, "test", "pass", "all green"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApproval(id, "code_review", "alice", "approved", "lgtm"); err != nil {
		t.Fatal(err)
	}
	return s, id
}

// tamper bypasses the append-only triggers the way someone with file access
// could (drop the trigger, edit the row).
func tamper(t *testing.T, s *Store, stmts ...string) {
	t.Helper()
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestRecordsVerifyCleanWhenUntouched(t *testing.T) {
	s, _ := sealedStore(t)
	r, err := s.VerifyRecords()
	if err != nil || !r.OK() || r.Checked != 2 || r.Unsealed != 0 {
		t.Fatalf("VerifyRecords = %+v, %v", r, err)
	}
	if c, err := s.VerifyChain(); err != nil || !c.OK() {
		t.Fatalf("chain: %+v, %v", c, err)
	}
}

func TestEditedApprovalIsDetected(t *testing.T) {
	s, _ := sealedStore(t)
	// the attack the seal exists for: rewrite a rejection/approval after the fact
	tamper(t, s, `DROP TRIGGER approvals_no_update`, `UPDATE approvals SET approver = 'mallory' WHERE id = 1`)
	r, _ := s.VerifyRecords()
	if r.OK() || r.BadRef != "approvals#1" {
		t.Fatalf("edit not detected: %+v", r)
	}
}

func TestEditedCheckIsDetected(t *testing.T) {
	s, _ := sealedStore(t)
	tamper(t, s, `DROP TRIGGER checks_no_update`, `UPDATE checks SET status = 'pass', detail = 'faked' WHERE id = 1`)
	if r, _ := s.VerifyRecords(); r.OK() || r.BadRef != "checks#1" {
		t.Fatalf("edit not detected: %+v", r)
	}
	tamper(t, s, `UPDATE checks SET status = 'pass', detail = 'all green' WHERE id = 1`) // restoring the original content verifies again
	if r, _ := s.VerifyRecords(); !r.OK() {
		t.Fatalf("restored content should verify: %+v", r)
	}
}

func TestDeletedRecordIsDetected(t *testing.T) {
	s, _ := sealedStore(t)
	tamper(t, s, `DROP TRIGGER approvals_no_delete`, `DELETE FROM approvals WHERE id = 1`)
	r, _ := s.VerifyRecords()
	if r.OK() || r.BadRef != "approvals#1" {
		t.Fatalf("deletion not detected: %+v", r)
	}
}

// Rows written before sealing existed are reported, not treated as tampering.
func TestLegacyRowsAreUnsealedNotBad(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	tamper(t, s, `INSERT INTO checks (task_id, kind, status, actor_type, actor_id, created_at) VALUES (1, 'test', 'pass', 'human', 'x', '2020-01-01T00:00:00Z')`)
	_ = id
	r, err := s.VerifyRecords()
	if err != nil || !r.OK() || r.Unsealed != 1 {
		t.Fatalf("VerifyRecords = %+v, %v", r, err)
	}
}

// A forged seal can't be added through `acline log`, and editing the seal event
// breaks the hash chain that protects it.
func TestSealsAreChainProtectedAndNotUserLoggable(t *testing.T) {
	s, _ := sealedStore(t)
	if UserLogTypes["row_seal"] {
		t.Fatal("row_seal must not be a user-loggable event type")
	}
	tamper(t, s, `DROP TRIGGER events_no_update`, `UPDATE events SET message = 'checks#1 sha256:0000000000000000000000000000000000000000000000000000000000000000' WHERE type = 'row_seal' AND message LIKE 'checks#1%'`)
	if c, _ := s.VerifyChain(); c.OK() {
		t.Fatal("rewriting a seal event must break the hash chain")
	}
}

func TestFailedSealRollsBackTheRecord(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	tamper(t, s, `CREATE TRIGGER no_events BEFORE INSERT ON events BEGIN SELECT RAISE(ABORT,'no'); END`)
	if _, err := s.AddCheck(id, "test", "pass", ""); err == nil {
		t.Fatal("expected failure")
	}
	if n := count(t, s, `SELECT COUNT(*) FROM checks`); n != 0 {
		t.Errorf("an unsealed check was left behind (%d rows)", n)
	}
}

// Blanking the hash of the newest events turned them into "legacy,
// unhashed" rows, which verify skipped, so they could then be edited freely.
func TestUnhashedEventsAfterHashingBeganAreTampering(t *testing.T) {
	h := humanStore(t)
	for i := 0; i < 3; i++ {
		if _, err := h.LogEvent(nil, nil, "note", "event"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.DB.Exec(`DROP TRIGGER events_no_update`); err != nil { // an attacker with file access
		t.Fatal(err)
	}
	if _, err := h.DB.Exec(`UPDATE events SET hash = NULL, prev_hash = NULL, message = 'rewritten' WHERE id = (SELECT MAX(id) FROM events)`); err != nil {
		t.Fatal(err)
	}
	res, err := h.VerifyChain()
	if err != nil {
		t.Fatal(err)
	}
	if res.OK() {
		t.Fatalf("an unhashed event after hashed ones passed verification: %+v", res)
	}
}

// Rows from before hashing existed (a leading run) are still reported, not failed.
func TestLeadingUnhashedEventsAreLegacyNotTampering(t *testing.T) {
	h := humanStore(t)
	if _, err := h.DB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DB.Exec(`INSERT INTO events (type, message, created_at) VALUES ('note', 'old', '2020-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LogEvent(nil, nil, "note", "new"); err != nil {
		t.Fatal(err)
	}
	res, err := h.VerifyChain()
	if err != nil || !res.OK() || res.Unhashed != 1 {
		t.Fatalf("legacy prefix: %+v, %v", res, err)
	}
}

// events.role_id was outside the hash, so role attribution on the trail
// could be changed without verify noticing.
func TestChangingAnEventsRoleIsDetected(t *testing.T) {
	h := humanStore(t)
	r, err := h.GetRoleByName(nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := h.GetRoleByName(nil, "manager")
	if _, err := h.LogEventWithRole(nil, nil, &r.ID, "note", "checked by qa"); err != nil {
		t.Fatal(err)
	}
	if res, err := h.VerifyChain(); err != nil || !res.OK() {
		t.Fatalf("before tampering: %+v, %v", res, err)
	}
	if _, err := h.DB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.DB.Exec(`UPDATE events SET role_id = ? WHERE message = 'checked by qa'`, other.ID); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.VerifyChain(); res.OK() {
		t.Fatalf("a changed role_id went unnoticed: %+v", res)
	}
}

// Events hashed before role_id was covered must keep verifying, and the switch
// must not be reversible by deleting the marker.
func TestEventHashVersionMarkerKeepsOldEventsValidAndCannotBeRemoved(t *testing.T) {
	h := humanStore(t)
	// An event written the old way (v1 hash, no marker yet), as an older acline did.
	var prev string
	h.DB.QueryRow(`SELECT COALESCE(hash,'') FROM events ORDER BY id DESC LIMIT 1`).Scan(&prev)
	old := eventHash(prev, nil, nil, "note", "legacy", "human", "tester", "", "2026-01-01T00:00:00Z")
	if _, err := h.DB.Exec(`INSERT INTO events (type, message, actor_type, actor_id, created_at, prev_hash, hash) VALUES ('note', 'legacy', 'human', 'tester', '2026-01-01T00:00:00Z', ?, ?)`, prev, old); err != nil {
		t.Fatal(err)
	}
	if _, err := h.LogEvent(nil, nil, "note", "new"); err != nil {
		t.Fatal(err)
	}
	if res, err := h.VerifyChain(); err != nil || !res.OK() {
		t.Fatalf("old and new events together: %+v, %v", res, err)
	}
	var markers int
	h.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'hash_version'`).Scan(&markers)
	if markers != 1 {
		t.Fatalf("hash_version markers = %d, want 1", markers)
	}
	h.DB.Exec(`DROP TRIGGER events_no_delete`)
	if _, err := h.DB.Exec(`DELETE FROM events WHERE type = 'hash_version'`); err != nil {
		t.Fatal(err)
	}
	if res, _ := h.VerifyChain(); res.OK() {
		t.Fatal("removing the hash-version marker went unnoticed")
	}
}
