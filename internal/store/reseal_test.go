package store

import (
	"errors"
	"testing"
)

// legacyStore is a store as an old build left it: events with no hash, and a
// check recorded before sealing existed, followed by current activity (which
// writes the hash_version marker and the seal watermark).
func legacyStore(t *testing.T) *Store {
	t.Helper()
	s := humanStore(t)
	if n, _ := s.CountEventsAfter(0, hashVersionEventType); n != 0 {
		t.Fatalf("premise: a fresh store has no marker yet (found %d)", n)
	}
	tamper(t, s,
		`INSERT INTO events (type, message, actor_type, actor_id, created_at) VALUES ('note', 'from an old build', 'human', 'old', '2020-01-01T00:00:00Z')`,
		`INSERT INTO events (type, message, actor_type, actor_id, created_at) VALUES ('note', 'also old', 'human', 'old', '2020-01-02T00:00:00Z')`,
		`INSERT INTO tasks (title, status, priority, risk, autonomy, created_at, updated_at) VALUES ('old task', 'todo', 'normal', 'low', 'hotl', '2020-01-01T00:00:00Z', '2020-01-01T00:00:00Z')`,
		`INSERT INTO checks (task_id, kind, status, actor_type, actor_id, created_at) VALUES (1, 'test', 'pass', 'human', 'old', '2020-01-03T00:00:00Z')`,
	)
	id, err := s.AddTask("new task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResealTurnsToleratedLegacyIntoCheckedRecords(t *testing.T) {
	s := legacyStore(t)
	c, err := s.VerifyChain()
	if err != nil || !c.OK() || c.Unhashed != 2 || !c.Legacy() {
		t.Fatalf("before: chain %+v, %v", c, err)
	}
	if r, _ := s.VerifyRecords(); !r.OK() || r.Unsealed != 1 {
		t.Fatalf("before: records %+v", r)
	}

	agent := *s
	agent.Actor = Actor{Type: "agent", ID: "claude"}
	if _, err := agent.Reseal(""); !errors.Is(err, ErrAgentCannotReseal) {
		t.Fatalf("agent reseal: err = %v", err)
	}

	res, err := s.Reseal("")
	if err != nil || res.Sealed != 1 || res.Unhashed != 2 || res.Attested != 2 {
		t.Fatalf("reseal: %+v, %v", res, err)
	}
	c, _ = s.VerifyChain()
	if !c.OK() || !c.Resealed || c.Legacy() {
		t.Fatalf("after: chain %+v", c)
	}
	if r, _ := s.VerifyRecords(); !r.OK() || r.Unsealed != 0 || r.Checked != 2 {
		t.Fatalf("after: records %+v", r)
	}
	if again, err := s.Reseal(""); err != nil || !again.AlreadyOK {
		t.Fatalf("second reseal: %+v, %v", again, err)
	}
}

func TestAfterResealLegacyEditsAndUnsealedInsertsFail(t *testing.T) {
	t.Run("an unhashed event's content", func(t *testing.T) {
		s := legacyStore(t)
		if _, err := s.Reseal(""); err != nil {
			t.Fatal(err)
		}
		tamper(t, s, `DROP TRIGGER events_no_update`, `UPDATE events SET message = 'rewritten' WHERE message = 'from an old build'`)
		if c, _ := s.VerifyChain(); c.OK() {
			t.Fatal("editing an attested unhashed event must fail verify")
		}
	})
	t.Run("an old event's role", func(t *testing.T) {
		s := legacyStore(t)
		if _, err := s.Reseal(""); err != nil {
			t.Fatal(err)
		}
		tamper(t, s, `DROP TRIGGER events_no_update`, `UPDATE events SET role_id = 1 WHERE message = 'also old'`)
		if c, _ := s.VerifyChain(); c.OK() {
			t.Fatal("changing a pre-marker event's role must fail verify")
		}
	})
	t.Run("an unsealed record below the watermark", func(t *testing.T) {
		s := legacyStore(t)
		if _, err := s.Reseal(""); err != nil {
			t.Fatal(err)
		}
		tamper(t, s, `INSERT INTO checks (id, task_id, kind, status, actor_type, actor_id, created_at) VALUES (0, 1, 'test', 'pass', 'human', 'x', '2020-01-01T00:00:00Z')`)
		if r, _ := s.VerifyRecords(); r.OK() {
			t.Fatal("an unsealed record after a reseal must fail, whatever its id")
		}
	})
}

func TestResealRefusesAStoreThatDoesNotVerify(t *testing.T) {
	s := legacyStore(t)
	tamper(t, s, `DROP TRIGGER events_no_update`, `UPDATE events SET message = 'x' WHERE type = 'row_seal'`)
	if _, err := s.Reseal(""); !errors.Is(err, ErrResealNeedsCleanStore) {
		t.Fatalf("reseal of a tampered store: err = %v", err)
	}
	if n, _ := s.CountEventsAfter(0, legacyAttestedEvent); n != 0 {
		t.Fatal("a refused reseal wrote an attestation")
	}
}

func TestAFreshStoreHasNoLegacy(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.AddCheck(id, "test", "pass", ""); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.VerifyChain(); !c.OK() || c.Legacy() || c.PreMarker != 0 {
		t.Fatalf("fresh store: %+v", c)
	}
}
