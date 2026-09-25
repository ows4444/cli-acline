package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Approvals and checks are the evidence the completion gate rests on, but they
// were not part of the audit hash chain: only the `events` table was, and the
// append-only protection on approvals/checks is a SQLite trigger that anyone
// with file access can drop. So each approval/check is now *sealed*: when the
// row is inserted, a `row_seal` event -- inside the hash chain, written in the
// same transaction -- records a digest of the row's content. VerifyRecords
// recomputes the digests, so an edited row (digest mismatch) or a deleted row
// (seal with no row) is detected, and the chain in turn protects the seals.
//
// Rows recorded before sealing existed have no seal; they are reported as
// "unsealed", not as tampering.

// digest length-prefixes each field, like eventHash, so no two different
// records can produce the same byte stream.
func digest(fields ...string) string {
	var b strings.Builder
	for _, f := range fields {
		fmt.Fprintf(&b, "%d:%s|", len(f), f)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func roleStr(roleID *int64) string {
	if roleID == nil {
		return ""
	}
	return strconv.FormatInt(*roleID, 10)
}

func approvalDigest(id, taskID int64, kind, approver, decision, note, actorType, actorID, model, role, createdAt string) string {
	return digest("approvals", strconv.FormatInt(id, 10), strconv.FormatInt(taskID, 10), kind, approver, decision, note, actorType, actorID, model, role, createdAt)
}

// checkDigest seals a check. A check with no extra provenance (a hand-recorded
// one with no tree, which is every check written before source and tree_hash
// existed) keeps the original field list, so its seal still verifies; anything
// else also covers source and tree_hash, so changing either is detected.
func checkDigest(id, taskID int64, kind, status, detail, actorType, actorID, role, createdAt, source, tree string) string {
	fields := []string{"checks", strconv.FormatInt(id, 10), strconv.FormatInt(taskID, 10), kind, status, detail, actorType, actorID, role, createdAt}
	if source != CheckSourceManual || tree != "" {
		fields = append(fields, source, tree)
	}
	return digest(fields...)
}

// sealRow appends the row_seal event for a record inside the caller's tx.
func (s *Store) sealRow(tx *sql.Tx, table string, id, taskID int64, sessionID *int64, dig string) error {
	_, err := s.logEventTx(tx, &taskID, sessionID, nil, "row_seal", fmt.Sprintf("%s#%d sha256:%s", table, id, dig))
	return err
}

// The seal watermark is what turns "no seal" from a benign gap into evidence.
// Approvals and checks recorded before sealing existed have no seal and cannot
// be told from records inserted straight into the database. So when the first
// sealed record is written, a `seal_watermark` event -- inside the hash chain --
// notes the highest approval and check ids that exist at that moment. From then
// on an unsealed record with a higher id can only have been inserted outside
// acline (a direct write, a snapshot import), and VerifyRecords reports it.
//
// Limit: a record forged *before* the first sealed insert is grandfathered in
// with the legacy rows; the watermark cannot see back past its own creation.
var sealWatermarkRe = regexp.MustCompile(`^seal_watermark approvals<=(\d+) checks<=(\d+)$`)

// ensureSealWatermark writes the watermark event if none exists yet. It must run
// before the caller inserts its own record, so that record is above the line.
func (s *Store) ensureSealWatermark(tx *sql.Tx, sessionID *int64) error {
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE type = 'seal_watermark'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var maxApproval, maxCheck int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM approvals`).Scan(&maxApproval); err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM checks`).Scan(&maxCheck); err != nil {
		return err
	}
	_, err := s.logEventTx(tx, nil, sessionID, nil, "seal_watermark",
		fmt.Sprintf("seal_watermark approvals<=%d checks<=%d", maxApproval, maxCheck))
	return err
}

func (s *Store) currentSessionID() *int64 {
	if sess, err := s.CurrentSession(); err == nil {
		return &sess.ID
	}
	return nil
}

// RecordsResult reports the integrity of sealed approvals and checks.
type RecordsResult struct {
	Checked  int    // rows whose seal matched
	Unsealed int    // rows that predate sealing (not an error)
	BadRef   string // first tampered record, e.g. "approvals#7" ("" = none)
	Reason   string
}

func (r RecordsResult) OK() bool { return r.BadRef == "" }

var sealRe = regexp.MustCompile(`^(approvals|checks)#(\d+) sha256:([0-9a-f]{64})$`)

// VerifyRecords checks every approval and check against its seal.
func (s *Store) VerifyRecords() (RecordsResult, error) {
	var r RecordsResult
	seals := map[string]string{}
	watermark := map[string]int64{} // table -> highest id that may legitimately be unsealed
	resealed := false               // after Reseal no record may be unsealed
	rows, err := s.DB.Query(`SELECT type, message FROM events WHERE type IN ('row_seal', 'seal_watermark', 'legacy_attested') ORDER BY id`)
	if err != nil {
		return r, err
	}
	for rows.Next() {
		var typ, msg string
		if err := rows.Scan(&typ, &msg); err != nil {
			rows.Close()
			return r, err
		}
		if typ == legacyAttestedEvent {
			resealed = true
			continue
		}
		if typ == "seal_watermark" {
			if m := sealWatermarkRe.FindStringSubmatch(msg); m != nil && len(watermark) == 0 { // the first one is the line
				a, _ := strconv.ParseInt(m[1], 10, 64)
				c, _ := strconv.ParseInt(m[2], 10, 64)
				watermark["approvals"], watermark["checks"] = a, c
			}
			continue
		}
		if m := sealRe.FindStringSubmatch(msg); m != nil {
			if _, dup := seals[m[1]+"#"+m[2]]; !dup {
				seals[m[1]+"#"+m[2]] = m[3]
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return r, err
	}
	rows.Close()

	seen := map[string]bool{}
	check := func(table string, id int64, got string) bool {
		ref := fmt.Sprintf("%s#%d", table, id)
		want, sealed := seals[ref]
		if !sealed {
			if resealed {
				r.BadRef, r.Reason = ref, "record has no seal but every record was sealed when the store was resealed: it was inserted outside acline"
				return false
			}
			if line, started := watermark[table]; started && id > line {
				r.BadRef, r.Reason = ref, "record has no seal but was created after sealing began: it was inserted outside acline (a direct database write or a snapshot import)"
				return false
			}
			r.Unsealed++
			return true
		}
		seen[ref] = true
		if want != got {
			r.BadRef, r.Reason = ref, "record content does not match its seal: it was altered after being recorded"
			return false
		}
		r.Checked++
		return true
	}

	ar, err := s.DB.Query(`SELECT id, task_id, kind, approver, decision, COALESCE(note,''), COALESCE(actor_type,''), COALESCE(actor_id,''),
		COALESCE(model,''), CAST(COALESCE(role_id,'') AS TEXT), created_at FROM approvals ORDER BY id`)
	if err != nil {
		return r, err
	}
	for ar.Next() {
		var id, taskID int64
		var kind, approver, decision, note, at, aid, model, role, created string
		if err := ar.Scan(&id, &taskID, &kind, &approver, &decision, &note, &at, &aid, &model, &role, &created); err != nil {
			ar.Close()
			return r, err
		}
		if !check("approvals", id, approvalDigest(id, taskID, kind, approver, decision, note, at, aid, model, role, created)) {
			ar.Close()
			return r, nil
		}
	}
	ar.Close()

	cr, err := s.DB.Query(`SELECT id, task_id, kind, status, COALESCE(detail,''), COALESCE(actor_type,''), COALESCE(actor_id,''),
		CAST(COALESCE(role_id,'') AS TEXT), created_at, source, COALESCE(tree_hash,'') FROM checks ORDER BY id`)
	if err != nil {
		return r, err
	}
	for cr.Next() {
		var id, taskID int64
		var kind, status, detail, at, aid, role, created, source, tree string
		if err := cr.Scan(&id, &taskID, &kind, &status, &detail, &at, &aid, &role, &created, &source, &tree); err != nil {
			cr.Close()
			return r, err
		}
		if !check("checks", id, checkDigest(id, taskID, kind, status, detail, at, aid, role, created, source, tree)) {
			cr.Close()
			return r, nil
		}
	}
	cr.Close()

	for ref := range seals {
		if !seen[ref] {
			r.BadRef, r.Reason = ref, "a sealed record is missing: it was deleted after being recorded"
			return r, nil
		}
	}
	return r, nil
}
