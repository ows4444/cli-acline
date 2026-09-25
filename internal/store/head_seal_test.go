package store

import (
	"database/sql"
	"errors"
	"testing"
)

// rehashChain is the attacker the seal exists for: someone with the database
// file who edits a row and then recomputes every hash so VerifyChain passes.
func rehashChain(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB.Exec(`DROP TRIGGER IF EXISTS events_no_update`); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query(`SELECT id, task_id, session_id, type, message, COALESCE(actor_type,''), COALESCE(actor_id,''),
		COALESCE(model,''), created_at, CAST(COALESCE(role_id,'') AS TEXT) FROM events WHERE COALESCE(hash,'') != '' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id                                                 int64
		task, session                                      sql.NullInt64
		typ, msg, actorType, actorID, model, created, role string
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.task, &r.session, &r.typ, &r.msg, &r.actorType, &r.actorID, &r.model, &r.created, &r.role); err != nil {
			t.Fatal(err)
		}
		all = append(all, r)
	}
	rows.Close()
	prev, v2 := "", false
	for _, r := range all {
		var tp, sp *int64
		if r.task.Valid {
			tp = &r.task.Int64
		}
		if r.session.Valid {
			sp = &r.session.Int64
		}
		h := eventHash(prev, tp, sp, r.typ, r.msg, r.actorType, r.actorID, r.model, r.created)
		if v2 {
			h = eventHashV2(prev, tp, sp, r.typ, r.msg, r.actorType, r.actorID, r.model, r.created, r.role)
		}
		if _, err := s.DB.Exec(`UPDATE events SET prev_hash = ?, hash = ? WHERE id = ?`, prev, h, r.id); err != nil {
			t.Fatal(err)
		}
		if r.typ == hashVersionEventType && r.msg == hashVersionV2Message {
			v2 = true
		}
		prev = h
	}
}

func tokenStore(t *testing.T) (*Store, string) {
	t.Helper()
	s := humanStore(t)
	token, err := s.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.LogEventGlobal("note", "work")
	}
	return s, token
}

func TestSealingNeedsAnEnabledTokenAndTheTokenItself(t *testing.T) {
	if _, err := humanStore(t).SealHead(""); !errors.Is(err, ErrNoApprovalToken) {
		t.Fatalf("no token enabled: err = %v, want ErrNoApprovalToken", err)
	}
	s, _ := tokenStore(t)
	if _, err := s.SealHead(""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("no token given: err = %v", err)
	}
	if _, err := s.SealHead("acl_wrong"); !errors.Is(err, ErrApprovalTokenInvalid) {
		t.Fatalf("wrong token: err = %v", err)
	}
	if n, _ := s.CountEventsAfter(0, headSealEvent); n != 0 {
		t.Fatalf("a refused seal wrote %d seal event(s)", n)
	}
}

func TestAHeadSealDetectsARewrittenAndRehashedTrail(t *testing.T) {
	s, token := tokenStore(t)
	sealed, err := s.SealHead(token)
	if err != nil {
		t.Fatal(err)
	}
	s.LogEventGlobal("note", "later work is fine")
	if res, err := s.CheckHeadSeal(token); err != nil || !res.Sealed || res.Head != sealed {
		t.Fatalf("fresh seal: %+v, %v", res, err)
	}

	// Rewrite an event before the sealed head and recompute the whole chain.
	if _, err := s.DB.Exec(`DROP TRIGGER IF EXISTS events_no_update; UPDATE events SET message = 'nothing to see' WHERE id = (SELECT MIN(id) FROM events WHERE type = 'note')`); err != nil {
		t.Fatal(err)
	}
	rehashChain(t, s)
	if r, err := s.VerifyChain(); err != nil || !r.OK() {
		t.Fatalf("premise: the recomputed chain verifies on its own: %+v, %v", r, err)
	}
	if _, err := s.CheckHeadSeal(token); !errors.Is(err, ErrHeadSealInvalid) {
		t.Fatalf("the seal must catch a rewritten, rehashed trail: err = %v", err)
	}
}

func TestASealUnderAMadeUpKeyCannotStandInForTheRealOne(t *testing.T) {
	s, token := tokenStore(t)
	if _, err := s.SealHead(token); err != nil {
		t.Fatal(err)
	}
	// A seal an attacker appends under a key id of their choosing is not trusted...
	if _, err := s.LogEvent(nil, nil, headSealEvent, "head 1:"+hashToken("x")+" key=000000000000 mac="+hashToken("y")); err != nil {
		t.Fatal(err)
	}
	res, err := s.CheckHeadSeal(token)
	if err != nil || !res.Sealed || res.Foreign != 1 {
		t.Fatalf("the real seal must still be checked and the other counted: %+v, %v", res, err)
	}
	// ...and one under the real key id but without the token fails outright.
	if _, err := s.LogEvent(nil, nil, headSealEvent, "head 1:"+hashToken("x")+" key="+headSealKeyID(token)+" mac="+hashToken("y")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CheckHeadSeal(token); !errors.Is(err, ErrHeadSealInvalid) {
		t.Fatalf("a forged seal under the real key id: err = %v", err)
	}
}

func TestRotatingTheTokenReseals(t *testing.T) {
	s, token := tokenStore(t)
	if _, err := s.SealHead(token); err != nil {
		t.Fatal(err)
	}
	next, err := s.RotateApprovalToken(token)
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.CheckHeadSeal(next)
	if err != nil || !res.Sealed || res.Foreign != 0 {
		t.Fatalf("after rotation the newest seal is the new token's: %+v, %v", res, err)
	}
}
