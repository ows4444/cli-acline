package tui

import (
	"strings"
	"testing"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

// tasksSession opens the shell on the Tasks tab with three tasks: #1 login
// (high, cli), #2 docs (low, docs), #3 cancelled (low, cli).
func tasksSession(t *testing.T, projectID *int64, st *store.Store) *tuitest.Session {
	t.Helper()
	m, err := newShell(st, projectID, noHash, tasksScreen{})
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 140, 30)
	t.Cleanup(s.Close)
	return s
}

func seedTasks(t *testing.T, st *store.Store, pid *int64) {
	t.Helper()
	st.AddTask("fix login redirect", "users bounce to /", "high", store.TaskOpts{Risk: "high", Area: "cli", ProjectID: pid})
	st.AddTask("write the docs", "", "normal", store.TaskOpts{Area: "docs", ProjectID: pid})
	done, _ := st.AddTask("old cleanup", "", "normal", store.TaskOpts{Area: "cli", ProjectID: pid})
	if _, err := st.UpdateTask(done, store.TaskUpdate{Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
}

func TestTasksFilterByStatusRiskAndArea(t *testing.T) {
	st := openTestStore(t)
	seedTasks(t, st, nil)
	s := tasksSession(t, nil, st)
	shows(t, s, "status: open", "2 of 3 tasks", "fix login redirect", "write the docs")
	lacks(t, s, "old cleanup")

	s.Keys("f") // all
	shows(t, s, "status: all", "3 of 3 tasks", "old cleanup")
	s.Keys("R", "R", "R") // high
	shows(t, s, "risk: high", "1 of 3 tasks", "fix login redirect")
	lacks(t, s, "write the docs")
	s.Keys("x", "a", "a") // areas cycle: any, cli, docs
	shows(t, s, "area: docs", "write the docs")
	lacks(t, s, "fix login redirect")
}

func TestTasksSearchTakesTypedKeys(t *testing.T) {
	st := openTestStore(t)
	seedTasks(t, st, nil)
	s := tasksSession(t, nil, st)
	s.Keys("/", "bounce q") // q is typed into the search, not quit
	if s.Done() {
		t.Fatal("q in the search box quit the TUI")
	}
	lacks(t, s, "write the docs")
	s.Keys("backspace", "backspace") // "bounce": matches the login task's description
	shows(t, s, "fix login redirect")
	lacks(t, s, "write the docs")
	s.Keys("enter") // keep the search, leave the box: keys are shortcuts again
	s.Keys("f")
	shows(t, s, "status: all")
	s.Keys("/", "esc") // esc clears it
	shows(t, s, "write the docs")
}

func TestTasksSortSurvivesAFilterChange(t *testing.T) {
	st := openTestStore(t)
	for _, title := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		st.AddTask("task "+title, "", "normal", store.TaskOpts{})
	}
	s := tasksSession(t, nil, st)
	s.Keys("s", "s") // ID descending
	first := func() string {
		for _, l := range s.Screen() {
			f := strings.Fields(strings.TrimPrefix(strings.TrimSpace(l), ">"))
			if len(f) > 0 && (f[0] == "11" || f[0] == "1") {
				return f[0]
			}
		}
		return ""
	}
	if got := first(); got != "11" {
		t.Fatalf("after sorting descending the first ID is %q:\n%s", got, rendered(s))
	}
	s.Keys("f") // a filter change keeps the order
	if got := first(); got != "11" {
		t.Fatalf("a filter change lost the sort: first ID %q:\n%s", got, rendered(s))
	}
}

func TestTasksShowTheProjectColumnOnlyAcrossProjects(t *testing.T) {
	st := openTestStore(t)
	pid, _ := st.AddProject("api", t.TempDir(), "hotl")
	seedTasks(t, st, &pid)
	s := tasksSession(t, nil, st)
	shows(t, s, "Project", "api")
	s2 := tasksSession(t, &pid, st)
	lacks(t, s2, "Project")
}

func TestEnterOnATaskAsksToOpenIt(t *testing.T) {
	st := openTestStore(t)
	seedTasks(t, st, nil)
	sc, err := tasksScreen{}.load(env{st: st, hash: noHash})
	if err != nil {
		t.Fatal(err)
	}
	sc, _ = sc.update(sizeMsg{140, 20})
	sc, _ = sc.update(tk.Key{Type: tk.KeyDown})
	_, cmd := sc.update(tk.Key{Type: tk.KeyEnter})
	for cmd != nil { // the table answers with SelectedMsg, the screen with openTaskMsg
		msg := cmd()
		if open, ok := msg.(openTaskMsg); ok {
			if open.id != 2 {
				t.Fatalf("opened #%d, want #2", open.id)
			}
			return
		}
		_, cmd = sc.update(msg)
	}
	t.Fatal("enter did not ask to open the task")
}
