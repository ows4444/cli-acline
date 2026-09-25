package store

import (
	"errors"
	"testing"
	"time"
)

func TestBeginSessionIsAtomicAndSingle(t *testing.T) {
	s := humanStore(t)
	pid, _ := s.AddProject("p", "/tmp/p", "hotl")
	id, _ := s.AddTask("t", "", "normal", TaskOpts{ProjectID: &pid})

	sid, err := s.BeginSession(SessionStart{TaskID: &id})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ := s.CurrentSession()
	if sess.ID != sid || !sess.ProjectID.Valid || sess.ProjectID.Int64 != pid {
		t.Errorf("session = %+v; want it linked to the task's project #%d", sess, pid)
	}
	if got, _ := s.GetTask(id); got.Status != "in_progress" {
		t.Errorf("task status = %q", got.Status)
	}
	if _, err := s.BeginSession(SessionStart{}); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("second start = %v, want ErrSessionActive", err)
	}
}

func TestBeginSessionFailureLeavesTheTaskAlone(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, err := s.DB.Exec(`CREATE TRIGGER no_sess BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT,'no'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginSession(SessionStart{TaskID: &id}); err == nil {
		t.Fatal("expected failure")
	}
	if got, _ := s.GetTask(id); got.Status != "todo" {
		t.Errorf("task left %q after a failed session start", got.Status)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM events`); n != 0 {
		t.Errorf("%d event(s) survived the rollback", n)
	}
}

// Ending a session discards its policy: with a token enabled, a confined
// actor must not be able to lift the confinement by ending it.
func TestEndingARestrictiveSessionNeedsTheTokenWhenEnabled(t *testing.T) {
	s := humanStore(t)
	token, _ := s.EnableApprovalToken()
	if _, err := s.BeginSession(SessionStart{Policy: Policy{DenyTools: []string{"Bash"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EndSessionWithToken("", SessionCost{}, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("end without token = %v", err)
	}
	if _, err := s.CurrentSession(); err != nil {
		t.Fatal("session must still be active")
	}
	if _, err := s.EndSessionWithToken("", SessionCost{}, token); err != nil {
		t.Fatalf("end with token = %v", err)
	}
	// a non-restrictive session ends freely even with a token enabled
	s.BeginSession(SessionStart{Policy: Policy{Label: "just a label"}})
	if _, err := s.EndSessionWithToken("", SessionCost{}, ""); err != nil {
		t.Fatalf("label-only session should end freely: %v", err)
	}
}

// One active session for the whole store meant project A's policy and role
// judged tool calls in project B, and a session in A blocked any in B.
func TestSessionsAreScopedToTheProjectTheDirectoryResolvesTo(t *testing.T) {
	s := humanStore(t)
	a, b := t.TempDir(), t.TempDir()
	pa, _ := s.AddProject("a", a, "hotl")
	pb, _ := s.AddProject("b", b, "hotl")

	sa, err := s.BeginSession(SessionStart{ProjectID: &pa, Policy: Policy{DenyTools: []string{"Bash"}}})
	if err != nil {
		t.Fatal(err)
	}
	sb, err := s.BeginSession(SessionStart{ProjectID: &pb})
	if err != nil {
		t.Fatalf("a session in b was blocked by a's: %v", err)
	}
	if _, err := s.BeginSession(SessionStart{ProjectID: &pa}); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("a second session in a = %v, want ErrSessionActive", err)
	}

	t.Chdir(a)
	if cur, err := s.CurrentSession(); err != nil || cur.ID != sa {
		t.Fatalf("in a: %+v, %v; want session #%d", cur, err, sa)
	}
	if ok, _, _ := s.CheckPolicy("Bash", ""); ok {
		t.Error("a's policy did not apply in a")
	}
	t.Chdir(b)
	if cur, err := s.CurrentSession(); err != nil || cur.ID != sb {
		t.Fatalf("in b: %+v, %v; want session #%d", cur, err, sb)
	}
	if ok, why, _ := s.CheckPolicy("Bash", ""); !ok {
		t.Errorf("a's policy applied in b: %s", why)
	}
}

func TestAnUnscopedSessionAppliesWhereNoProjectSessionIsActive(t *testing.T) {
	s := humanStore(t)
	dir := t.TempDir()
	if _, err := s.AddProject("p", dir, "hotl"); err != nil {
		t.Fatal(err)
	}
	id, err := s.BeginSession(SessionStart{Policy: Policy{DenyTools: []string{"Bash"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if cur, err := s.CurrentSession(); err != nil || cur.ID != id {
		t.Fatalf("an unscoped session must still confine a project directory: %+v, %v", cur, err)
	}
	pid, _ := s.GetProjectByName("p")
	if _, err := s.BeginSession(SessionStart{ProjectID: &pid.ID}); !errors.Is(err, ErrSessionActive) {
		t.Fatalf("a project session alongside an unscoped one = %v, want ErrSessionActive", err)
	}
}

// A crashed session is never ended automatically (that would lift its policy);
// it is reported so a person can end it.
func TestStaleSessionsAreReported(t *testing.T) {
	s := humanStore(t)
	id, _ := s.BeginSession(SessionStart{})
	if stale, _ := s.StaleSessions(time.Hour); len(stale) != 0 {
		t.Fatalf("a fresh session is stale: %+v", stale)
	}
	old := time.Now().Add(-3 * time.Hour).UTC().Format(time.RFC3339)
	if _, err := s.DB.Exec(`UPDATE sessions SET started_at = ? WHERE id = ?`, old, id); err != nil {
		t.Fatal(err)
	}
	stale, err := s.StaleSessions(time.Hour)
	if err != nil || len(stale) != 1 || stale[0].ID != id {
		t.Fatalf("stale = %+v, %v", stale, err)
	}
	// activity inside the session keeps it fresh
	if _, err := s.LogEvent(nil, &id, "note", "still here"); err != nil {
		t.Fatal(err)
	}
	if stale, _ := s.StaleSessions(time.Hour); len(stale) != 0 {
		t.Fatalf("a session with recent activity is stale: %+v", stale)
	}
}
