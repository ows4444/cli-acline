package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

func TestDashboardShowsWhatWaitsOnAPerson(t *testing.T) {
	st := openTestStore(t)
	id, _ := st.AddTask("ship the login fix", "", "normal", store.TaskOpts{Risk: "high"})
	if _, err := st.AddCheckWithMeta(id, nil, "test", "pass", "ran", "", store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:now"}); err != nil {
		t.Fatal(err)
	}
	st.AddSpec("payments v2", "body")
	st.AddMemory("cli", "lesson", "flags parse before env", store.MemoryOpts{ForcePending: true})

	m, err := newModel(st, nil, func(string) string { return "sha256:now" })
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 160, 40)
	defer s.Close()
	shows(t, s,
		"Audit trail", "intact:",
		"Awaiting your approval: the gate needs nothing else (1)", fmt.Sprintf("#%d [high risk] ship the login fix", id),
		"Draft specs (1)", "payments v2",
		"Memory awaiting review (1)", "flags parse before env",
		"Nothing waiting: Tasks needing attention, Plans awaiting a person, Proposed decisions",
	)
	// Already listed as awaiting approval, so not repeated under attention.
	if n := strings.Count(rendered(s), "ship the login fix"); n != 1 {
		t.Errorf("the task is listed %d times", n)
	}
}

// Titles and bodies are often written by agents; an escape sequence in one
// must not reach the person's terminal (it could retitle the window, write the
// clipboard, or redraw the screen to hide what is being approved).
func TestDashboardStripsEscapeSequencesFromStoredText(t *testing.T) {
	st := openTestStore(t)
	st.AddSpec("safe\x1b]52;c;cm0gLXJmIH4=\x07 title\x1b[2J\ttabbed\nnext", "body")
	m, err := newModel(st, nil, noHash)
	if err != nil {
		t.Fatal(err)
	}
	if v := m.View(); strings.Contains(v, "]52;") || strings.Contains(v, "\x1b[2J") || strings.Contains(v, "\x07") {
		t.Fatalf("an escape sequence from a spec title reached the screen: %q", v)
	}
	s := tuitest.New(m, 120, 30)
	defer s.Close()
	shows(t, s, "safe title tabbed next")
}

func TestDashboardCapsLongSectionsAndScrolls(t *testing.T) {
	st := openTestStore(t)
	for i := range 12 {
		st.AddMemory("cli", "lesson", fmt.Sprintf("entry %02d", i), store.MemoryOpts{ForcePending: true})
	}
	m, err := newModel(st, nil, noHash)
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 100, 12)
	defer s.Close()
	shows(t, s, "Audit trail")
	s.Keys("pgdown", "pgdown", "pgdown")
	shows(t, s, "… and 4 more", "Nothing waiting")
	lacks(t, s, "Audit trail", "entry 08")
	s.Keys("down", "down", "down") // past the end: stays on the last page
	shows(t, s, "Nothing waiting")
	s.Keys("g")
	shows(t, s, "Audit trail")
}

func TestHeaderStripsEscapeSequencesFromTheProjectName(t *testing.T) {
	st := openTestStore(t)
	pid, err := st.AddProject("api\x1b]0;pwned\x07", t.TempDir(), "hotl")
	if err != nil {
		t.Fatal(err)
	}
	m, err := newShell(st, &pid, noHash, fakeScreen{name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	if v := m.View(); strings.Contains(v, "]0;pwned") {
		t.Fatalf("an escape sequence in a project name reached the screen: %q", v)
	}
}
