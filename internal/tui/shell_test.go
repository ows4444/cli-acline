package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/tuitest"
	"github.com/ows4444/tui/widgets"

	"acline/internal/store"
)

// fakeScreen shows its name and the keys it was sent.
type fakeScreen struct {
	name string
	got  []string
}

func (f fakeScreen) title() string            { return f.name }
func (f fakeScreen) load(env) (screen, error) { return f, nil }
func (f fakeScreen) update(msg tk.Msg) (screen, tk.Cmd) {
	if k, ok := msg.(tk.Key); ok {
		f.got = append(f.got, k.String())
	}
	return f, nil
}
func (f fakeScreen) view(int, int) string {
	return "screen " + f.name + " got [" + strings.Join(f.got, " ") + "]"
}
func (f fakeScreen) keys() []widgets.Hint {
	return []widgets.Hint{{Key: "x", Action: f.name + " action"}}
}

func newSession(t *testing.T, st *store.Store, screens ...screen) *tuitest.Session {
	t.Helper()
	m, err := newShell(st, nil, noHash, screens...)
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 120, 30)
	t.Cleanup(s.Close)
	return s
}

func shows(t *testing.T, s *tuitest.Session, wants ...string) {
	t.Helper()
	got := rendered(s)
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("screen lacks %q:\n%s", w, got)
		}
	}
}

func lacks(t *testing.T, s *tuitest.Session, unwanted ...string) {
	t.Helper()
	got := rendered(s)
	for _, w := range unwanted {
		if strings.Contains(got, w) {
			t.Errorf("screen shows %q:\n%s", w, got)
		}
	}
}

func TestTabsSwitchScreensAndScreensGetOtherKeys(t *testing.T) {
	s := newSession(t, openTestStore(t), fakeScreen{name: "One"}, fakeScreen{name: "Two"}, fakeScreen{name: "Three"})
	shows(t, s, "[One]", "screen One", "[x] One action")
	s.Keys("tab")
	shows(t, s, "[Two]", "screen Two")
	s.Keys("shift+tab", "shift+tab")
	shows(t, s, "[Three]")
	s.Keys("1")
	shows(t, s, "[One]")
	s.Keys("9") // no ninth tab: nothing happens
	shows(t, s, "[One]")
	s.Keys("j", "enter")
	shows(t, s, "screen One got [j enter]")
}

func TestHelpListsTheScreensAndTheShellsKeys(t *testing.T) {
	s := newSession(t, openTestStore(t), fakeScreen{name: "One"})
	s.Keys("?")
	shows(t, s, "One action", "command palette", "switch project", "quit")
	s.Keys("esc")
	lacks(t, s, "command palette")
	shows(t, s, "screen One got []") // the key closed help; the screen never saw it
}

func TestPaletteListsEveryActionAndRunsTheChosenOne(t *testing.T) {
	s := newSession(t, openTestStore(t), fakeScreen{name: "One"}, fakeScreen{name: "Two"})
	s.Keys(":")
	shows(t, s, "Go to One", "Go to Two", "Switch project", "Refresh", "Help", "Quit")
	s.Keys("go to two", "enter")
	shows(t, s, "[Two]", "screen Two")
	lacks(t, s, "Run an action")

	s.Keys("ctrl+k")
	shows(t, s, "Run an action")
	s.Keys("esc")
	lacks(t, s, "Run an action")
	shows(t, s, "screen Two got []")
}

func TestProjectSwitcherRescopesTheShell(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.AddProject("api", t.TempDir(), "hotl"); err != nil {
		t.Fatal(err)
	}
	s := newSession(t, st, fakeScreen{name: "One"})
	shows(t, s, "acline · all projects")
	s.Keys("p")
	shows(t, s, "All projects", "api")
	s.Keys("down", "enter")
	shows(t, s, "acline · api ·")
	s.Keys("p", "up", "enter")
	shows(t, s, "acline · all projects")
	s.Keys("p", "esc")
	lacks(t, s, "Show which project")
}

func TestQuitFromThePalette(t *testing.T) {
	s := newSession(t, openTestStore(t), fakeScreen{name: "One"})
	s.Keys(":", "quit", "enter")
	for end := time.Now().Add(5 * time.Second); !s.Done() && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.Done() {
		t.Fatal("Quit from the palette did not quit")
	}
}

// Accessible mode prints every frame in turn; filler lines are noise there.
func TestLinearizeHasNoFillerLines(t *testing.T) {
	m, err := newShell(openTestStore(t), nil, noHash, fakeScreen{name: "One"})
	if err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 100, 40
	if got := m.Linearize(); strings.Contains(got, "\n\n\n") || strings.Contains(got, "\x1b[") {
		t.Errorf("Linearize has filler lines or escapes:\n%q", got)
	}
	if got := m.View(); strings.Count(got, "\n") != 39 {
		t.Errorf("View is %d lines, want the terminal's 40", strings.Count(got, "\n")+1)
	}
}

func TestATallScreenDoesNotPushTheHeaderOff(t *testing.T) {
	s := newSession(t, openTestStore(t), tallScreen{})
	s.Resize(80, 10)
	shows(t, s, "acline · all projects", "[q] quit", "row 0")
	lacks(t, s, "row 50")
}

type tallScreen struct{ fakeScreen }

func (tallScreen) view(int, int) string {
	var rows []string
	for i := range 100 {
		rows = append(rows, fmt.Sprintf("row %d", i))
	}
	return strings.Join(rows, "\n")
}
func (t tallScreen) load(env) (screen, error)       { return t, nil }
func (t tallScreen) update(tk.Msg) (screen, tk.Cmd) { return t, nil }
