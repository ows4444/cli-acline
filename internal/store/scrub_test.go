package store

import (
	"strings"
	"testing"
)

const testSecret = "sk-ant-api03-AbCdEfGhIjKlMnOpQrStUvWxYz0123456789_-abcdefgh"

// Every free-text write path must redact at the store, whoever the caller is.
func TestStoreRedactsSecretsOnEveryFreeTextWritePath(t *testing.T) {
	s := newTestStore(t, Actor{Type: "human", ID: "u"})

	noteID, err := s.AddNote(nil, "remember "+testSecret, "manual")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := s.AddTask("fix "+testSecret, "desc "+testSecret, "", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCheck(taskID, "test", "pass", "output "+testSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApproval(taskID, "code_review", "u", "approved", "ok "+testSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddEval(nil, nil, "suite", 0.9, 10, "note "+testSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LogEvent(nil, nil, "note", "logged "+testSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMemory("", "lesson", "mem "+testSecret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddDecision("dec "+testSecret, DecisionOpts{Context: testSecret, Decision: testSecret, Rationale: testSecret}); err != nil {
		t.Fatal(err)
	}
	specID, err := s.AddSpec("spec "+testSecret, "body "+testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReviseSpec(specID, "rev "+testSecret); err != nil {
		t.Fatal(err)
	}
	_ = noteID

	for _, q := range []string{
		`SELECT body FROM notes`, `SELECT title || COALESCE(description,'') FROM tasks`,
		`SELECT COALESCE(detail,'') FROM checks`, `SELECT COALESCE(note,'') FROM approvals`,
		`SELECT COALESCE(note,'') FROM evals`, `SELECT message FROM events`, `SELECT body FROM memory`,
		`SELECT title || COALESCE(context,'') || COALESCE(decision,'') || COALESCE(rationale,'') FROM decisions`,
		`SELECT title || COALESCE(body,'') FROM specs`,
	} {
		rows, err := s.DB.Query(q)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(v, "sk-ant-") {
				t.Errorf("secret persisted by %q: %q", q, v)
			}
		}
		rows.Close()
	}

	// Redacting before hashing keeps the audit chain valid.
	if res, err := s.VerifyChain(); err != nil || !res.OK() {
		t.Fatalf("chain after redacted writes: %+v, %v", res, err)
	}
}
