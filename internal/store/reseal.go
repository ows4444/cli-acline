package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

// Stores that predate hashing and sealing carry three kinds of record that
// `acline verify` can only tolerate, never check: events with no hash, events
// hashed before the hash_version marker (without their role_id), and
// approvals/checks with no seal. None can be fixed in place: hashing an old
// event means recomputing every hash after it, which would invalidate every
// external anchor and head seal.
//
// Reseal is a person's attestation instead. It verifies first (so it can't
// launder tampering), seals every unsealed approval and check, and records a
// legacy_attested event: a digest over the full content of the unhashed
// events and the role_id of every event before the marker. From then on,
// verify is strict: an unsealed record fails, and the pre-marker region must
// still match the attestation.

const legacyAttestedEvent = "legacy_attested"

var legacyAttestedRe = regexp.MustCompile(`^legacy_attested events<(\d+) unhashed=(\d+) sha256:([0-9a-f]{64})$`)

// ErrAgentCannotReseal: resealing vouches for records acline could not check,
// which is a person's call.
var ErrAgentCannotReseal = errors.New("an agent cannot reseal the store: resealing vouches for legacy records acline could not verify, so a person must do it")

// ErrResealNeedsCleanStore is returned when verification already fails.
var ErrResealNeedsCleanStore = errors.New("the store does not verify, so resealing would vouch for tampered records; fix what `acline verify` reports first")

// ResealResult is what Reseal did.
type ResealResult struct {
	Sealed    int   // approvals and checks sealed now
	Attested  int   // events before the hash_version marker covered by the attestation
	Unhashed  int   // of those, events with no hash
	MarkerID  int64 // the hash_version marker
	AlreadyOK bool  // the store was already resealed; nothing done
}

// Reseal seals the store's legacy records (see above). It needs a person, or
// an agent with the approval token.
func (s *Store) Reseal(token string) (ResealResult, error) {
	if err := s.requirePerson(token, ErrAgentCannotReseal); err != nil {
		return ResealResult{}, err
	}
	if c, err := s.VerifyChain(); err != nil {
		return ResealResult{}, err
	} else if !c.OK() {
		return ResealResult{}, fmt.Errorf("%w (event #%d: %s)", ErrResealNeedsCleanStore, c.BadID, c.Reason)
	}
	if r, err := s.VerifyRecords(); err != nil {
		return ResealResult{}, err
	} else if !r.OK() {
		return ResealResult{}, fmt.Errorf("%w (%s: %s)", ErrResealNeedsCleanStore, r.BadRef, r.Reason)
	}

	sessionID := s.currentSessionID()
	tx, err := s.DB.Begin()
	if err != nil {
		return ResealResult{}, err
	}
	defer tx.Rollback()
	var res ResealResult
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM events WHERE type = ?`, legacyAttestedEvent).Scan(&n); err != nil {
		return res, err
	}
	if n > 0 {
		res.AlreadyOK = true
		return res, nil
	}
	// The marker bounds the legacy region; make sure it exists before measuring it.
	if err := s.ensureEventHashV2(tx, sessionID); err != nil {
		return res, err
	}
	unsealed, err := unsealedRecords(tx)
	if err != nil {
		return res, err
	}
	for _, u := range unsealed {
		if err := s.sealRow(tx, u.table, u.id, u.taskID, sessionID, u.digest); err != nil {
			return res, err
		}
	}
	res.Sealed = len(unsealed)
	marker, count, unhashed, sum, err := legacyDigest(tx)
	if err != nil {
		return res, err
	}
	res.MarkerID, res.Attested, res.Unhashed = marker, count, unhashed
	msg := fmt.Sprintf("legacy_attested events<%d unhashed=%d sha256:%s", marker, unhashed, sum)
	if _, err := s.logEventTx(tx, nil, sessionID, nil, legacyAttestedEvent, msg); err != nil {
		return res, err
	}
	return res, tx.Commit()
}

// legacyDigest covers every event before the hash_version marker: the id and
// role_id of each (the old hash left role_id out), and every field of an
// unhashed one (no hash covers it at all).
func legacyDigest(q chainQuerier) (markerID int64, count, unhashed int, sum string, err error) {
	rows, err := q.Query(`SELECT id, CAST(COALESCE(task_id,'') AS TEXT), CAST(COALESCE(session_id,'') AS TEXT), type, message,
		COALESCE(actor_type,''), COALESCE(actor_id,''), COALESCE(model,''), created_at, COALESCE(hash,''),
		CAST(COALESCE(role_id,'') AS TEXT) FROM events ORDER BY id`)
	if err != nil {
		return 0, 0, 0, "", err
	}
	defer rows.Close()
	fields := []string{"legacy.v1"}
	for rows.Next() {
		var id int64
		var task, session, typ, msg, actorType, actorID, model, created, hash, role string
		if err := rows.Scan(&id, &task, &session, &typ, &msg, &actorType, &actorID, &model, &created, &hash, &role); err != nil {
			return 0, 0, 0, "", err
		}
		if typ == hashVersionEventType && msg == hashVersionV2Message {
			markerID = id
			break
		}
		count++
		fields = append(fields, strconv.FormatInt(id, 10), role)
		if hash == "" {
			unhashed++
			fields = append(fields, task, session, typ, msg, actorType, actorID, model, created)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, 0, 0, "", err
	}
	return markerID, count, unhashed, digest(fields...), nil
}

// checkLegacyAttestation compares the pre-marker region with the attestation,
// when there is one. It returns the attestation's event id (0 when the store
// was never resealed) and a reason when the region changed.
func checkLegacyAttestation(q chainQuerier) (attestID int64, reason string, err error) {
	rows, err := q.Query(`SELECT id, message FROM events WHERE type = ? ORDER BY id LIMIT 1`, legacyAttestedEvent)
	if err != nil {
		return 0, "", err
	}
	var msg string
	found := rows.Next()
	if found {
		err = rows.Scan(&attestID, &msg)
	}
	rows.Close()
	if err != nil || !found {
		return 0, "", err
	}
	m := legacyAttestedRe.FindStringSubmatch(msg)
	if m == nil {
		return attestID, "the legacy attestation is malformed", nil
	}
	marker, count, unhashed, sum, err := legacyDigest(q)
	if err != nil {
		return attestID, "", err
	}
	_ = count
	if strconv.FormatInt(marker, 10) != m[1] || strconv.Itoa(unhashed) != m[2] || sum != m[3] {
		return attestID, "events before the hash_version marker changed after the store was resealed (an unhashed event's content, or an event's role)", nil
	}
	return attestID, "", nil
}

type unsealedRecord struct {
	table      string
	id, taskID int64
	digest     string
}

// unsealedRecords lists the approvals and checks that have no row_seal.
func unsealedRecords(q chainQuerier) ([]unsealedRecord, error) {
	sealed := map[string]bool{}
	rows, err := q.Query(`SELECT message FROM events WHERE type = 'row_seal'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var msg string
		if err := rows.Scan(&msg); err != nil {
			rows.Close()
			return nil, err
		}
		if m := sealRe.FindStringSubmatch(msg); m != nil {
			sealed[m[1]+"#"+m[2]] = true
		}
	}
	rows.Close()

	var out []unsealedRecord
	collect := func(table, query string, scan func(*sql.Rows) (int64, int64, string, error)) error {
		rows, err := q.Query(query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			id, taskID, dig, err := scan(rows)
			if err != nil {
				return err
			}
			if !sealed[table+"#"+strconv.FormatInt(id, 10)] {
				out = append(out, unsealedRecord{table, id, taskID, dig})
			}
		}
		return rows.Err()
	}
	if err := collect("approvals", `SELECT id, task_id, kind, approver, decision, COALESCE(note,''), COALESCE(actor_type,''), COALESCE(actor_id,''),
		COALESCE(model,''), CAST(COALESCE(role_id,'') AS TEXT), created_at FROM approvals ORDER BY id`, func(r *sql.Rows) (int64, int64, string, error) {
		var id, taskID int64
		var kind, approver, decision, note, at, aid, model, role, created string
		err := r.Scan(&id, &taskID, &kind, &approver, &decision, &note, &at, &aid, &model, &role, &created)
		return id, taskID, approvalDigest(id, taskID, kind, approver, decision, note, at, aid, model, role, created), err
	}); err != nil {
		return nil, err
	}
	if err := collect("checks", `SELECT id, task_id, kind, status, COALESCE(detail,''), COALESCE(actor_type,''), COALESCE(actor_id,''),
		CAST(COALESCE(role_id,'') AS TEXT), created_at, source, COALESCE(tree_hash,'') FROM checks ORDER BY id`, func(r *sql.Rows) (int64, int64, string, error) {
		var id, taskID int64
		var kind, status, detail, at, aid, role, created, source, tree string
		err := r.Scan(&id, &taskID, &kind, &status, &detail, &at, &aid, &role, &created, &source, &tree)
		return id, taskID, checkDigest(id, taskID, kind, status, detail, at, aid, role, created, source, tree), err
	}); err != nil {
		return nil, err
	}
	return out, nil
}
