package tui

import (
	"strings"
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

func auditSession(t *testing.T, st *store.Store) *tuitest.Session {
	t.Helper()
	m, err := newShell(st, nil, noHash, auditScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 160, 40)
	t.Cleanup(s.Close)
	return s
}

func seedEvents(t *testing.T, st *store.Store) {
	t.Helper()
	a, _ := st.AddTask("first task", "", "normal", store.TaskOpts{})
	st.AddTask("second task", "", "normal", store.TaskOpts{})
	if _, _, err := st.AddCriterion(a, "When x, the system shall y"); err != nil {
		t.Fatal(err)
	}
}

func TestAuditShowsTheTrailAndFiltersIt(t *testing.T) {
	st := openTestStore(t)
	seedEvents(t, st)
	head, _, _ := st.ChainHead()
	s := auditSession(t, st)
	shows(t, s, "audit trail intact:", "head "+head.String(), "seal never sealed",
		"criterion_added", "task_created", "second task", "first task")

	s.Keys("y") // the first type in the trail
	shows(t, s, "type: criterion_added", "1 events")
	lacks(t, s, "second task")
	s.Keys("x", "t", "2", "enter")
	shows(t, s, "task: #2", "task #2 created: second task")
	lacks(t, s, "first task")
	s.Keys("x", "w", "w") // agent: none here
	shows(t, s, "actor: agent", "no event matches")
}

func TestAuditReportsATamperedTrail(t *testing.T) {
	st := openTestStore(t)
	seedEvents(t, st)
	if _, err := st.DB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE events SET message = 'rewritten' WHERE type = 'task_created' AND task_id = 1`); err != nil {
		t.Fatal(err)
	}
	s := auditSession(t, st)
	shows(t, s, "audit trail FAILED verification at event #")
}

func TestSealTheHeadAndCheckTheSealAndAnAnchor(t *testing.T) {
	st := openTestStore(t)
	seedEvents(t, st)
	token, err := st.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := st.ChainHead()
	s := auditSession(t, st)

	s.Keys("S", "y")
	shows(t, s, "Approval token for sealing the head")
	s.Keys(token, "enter")
	shows(t, s, "head sealed at event #", "seal newest is #")
	if strings.Contains(rendered(s), token[:8]) {
		t.Fatal("the token reached the screen")
	}

	s.Keys("c", "y", token, "enter")
	shows(t, s, "intact: nothing up to event #")

	s.Keys("A", head.String(), "enter")
	shows(t, s, "matches: nothing up to event #")
	s.Keys("A", "1:abc", "enter")
	shows(t, s, "invalid anchor")
}
