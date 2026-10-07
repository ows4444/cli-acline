package tui

import (
	"strings"
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

func detailSession(t *testing.T, w, h int) (*tuitest.Session, int64) {
	t.Helper()
	st := openTestStore(t)
	id, _ := st.AddTask("ship the login fix", "Users bounce to / after login.", "high", store.TaskOpts{Risk: "high", Area: "cli"})
	if _, err := st.AddCheckWithMeta(id, nil, "test", "pass", "ran", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:old"}); err != nil {
		t.Fatal(err)
	}
	st.AddCriterion(id, "When a user logs in, the system shall return them to the page they came from")
	m, err := newShell(st, nil, func(string) string { return "sha256:now" }, tasksScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, w, h)
	t.Cleanup(s.Close)
	s.Keys("enter")
	shows(t, s, "#1 ship the login fix") // the detail is open before the test reads the screen
	return s, id
}

func TestDetailShowsTheGateAndWhoCanClearEachBlocker(t *testing.T) {
	s, _ := detailSession(t, 160, 40)
	shows(t, s,
		"#1 ship the login fix", "risk      high", "Users bounce to / after login.",
		"[ ] #1 When a user logs in",
		"Gate: 2 blocker(s)", "(agent) the code has changed since the passing checks ran",
		"(person) human approval required",
		"STALE: about older code",
		"task_created",
	)
	lacks(t, s, "row_seal")
	s.Keys("esc")
	shows(t, s, "status: open", "1 of 1 tasks")
}

// A blocker says how to clear it at its end; cutting it at the column edge
// would hide exactly that.
func TestDetailWrapsLongLinesInsteadOfCuttingThem(t *testing.T) {
	s, _ := detailSession(t, 110, 40)
	// Read each column on its own: side by side, a screen row holds both.
	var left, right []string
	for _, l := range s.Screen() {
		if before, after, ok := strings.Cut(l, "│"); ok {
			left, right = append(left, before), append(right, after)
		}
	}
	join := func(col []string) string { return strings.Join(strings.Fields(strings.Join(col, " ")), " ") }
	if !strings.Contains(join(right), "acline approve 1 --kind code_review") {
		t.Errorf("the approval blocker was cut:\n%s", rendered(s))
	}
	if !strings.Contains(join(left), "return them to the page they came from") {
		t.Errorf("the criterion was cut:\n%s", rendered(s))
	}
}

func TestDetailStacksTheColumnsOnANarrowTerminal(t *testing.T) {
	s, _ := detailSession(t, 80, 60)
	lines := s.Screen()
	var title, gate int
	for i, l := range lines {
		if strings.Contains(l, "#1 ship the login fix") {
			title = i
		}
		if strings.Contains(l, "Gate:") {
			gate = i
		}
	}
	if gate <= title || strings.Contains(rendered(s), "│") {
		t.Errorf("at 80 columns the gate should follow the fields, not sit beside them:\n%s", rendered(s))
	}
}
