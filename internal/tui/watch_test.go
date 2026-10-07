package tui

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tk "github.com/ows4444/tui"
	"github.com/ows4444/tui/tuitest"
	"github.com/ows4444/tui/widgets"

	"acline/internal/store"
)

// otherConnection is the store as another process sees it: the CLI in a
// terminal, a hook, an agent.
func otherConnection(t *testing.T, st *store.Store) *store.Store {
	t.Helper()
	other, err := store.Open(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })
	other.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	return other
}

// liveSession runs the shell with the watch on a fake clock: the watch only
// ticks when the test advances it (a ticking real clock never lets the
// harness see a settled frame).
func liveSession(t *testing.T, st *store.Store, screens ...screen) *tuitest.Session {
	t.Helper()
	m, err := newShell(st, nil, noHash, screens...)
	if err != nil {
		t.Fatal(err)
	}
	m.watchEvery = time.Second
	s := tuitest.New(m, 140, 30, tuitest.WithClock(tuitest.NewFakeClock()))
	t.Cleanup(s.Close)
	return s
}

// tick lets the watch look once. Advance returns when the model has handled
// the tick, which can be before the frame is drawn; Send waits for the output
// to go quiet, so the screen read next is the one after the tick.
func tick(s *tuitest.Session) {
	s.Advance(time.Second)
	s.Advance(time.Second)
	s.Send(drawn{})
}

// drawn is a message no screen acts on.
type drawn struct{}

func TestTheScreenFollowsChangesMadeElsewhere(t *testing.T) {
	st := openTestStore(t)
	s := liveSession(t, st, tasksScreen{})
	shows(t, s, "0 of 0 tasks", "no active session")

	other := otherConnection(t, st)
	id, err := other.AddTask("added by an agent", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	tick(s)
	shows(t, s, "added by an agent", "1 of 1 tasks")
	if _, err := other.StartSession(&id, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	tick(s)
	shows(t, s, "session #1")
}

// countingScreen counts how often it is read.
type countingScreen struct {
	name  string
	loads *atomic.Int32
}

func (c countingScreen) title() string { return c.name }
func (c countingScreen) load(env) (screen, error) {
	c.loads.Add(1)
	return c, nil
}
func (c countingScreen) update(tk.Msg) (screen, tk.Cmd) { return c, nil }
func (c countingScreen) view(int, int) string           { return "screen " + c.name }
func (c countingScreen) keys() []widgets.Hint           { return nil }

func TestOnlyTheScreenShowingIsRereadAtOnce(t *testing.T) {
	st := openTestStore(t)
	var a, b atomic.Int32
	s := liveSession(t, st, countingScreen{"A", &a}, countingScreen{"B", &b})
	a0, b0 := a.Load(), b.Load()

	if _, err := otherConnection(t, st).AddTask("elsewhere", "", "normal", store.TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	tick(s)
	tick(s) // a second look finds nothing new
	if a.Load() != a0+1 || b.Load() != b0 {
		t.Fatalf("after a change: A read %d more, B %d more; want 1 and 0", a.Load()-a0, b.Load()-b0)
	}
	s.Keys("2")
	shows(t, s, "screen B")
	if b.Load() != b0+1 {
		t.Fatalf("B was read %d more times when shown, want 1 (it was stale)", b.Load()-b0)
	}
	s.Keys("1", "2") // nothing changed since: no reread
	if a.Load() != a0+1 || b.Load() != b0+1 {
		t.Fatalf("switching tabs with no change reread: A %d, B %d", a.Load()-a0, b.Load()-b0)
	}
}

// The TUI's own writes do not move data_version for its connection; it rereads
// after its own actions explicitly, so the watch does not reread them again.
func TestTheTUIsOwnWritesDoNotTriggerARereadOfTheirOwn(t *testing.T) {
	st := openTestStore(t)
	var a atomic.Int32
	s := liveSession(t, st, countingScreen{"A", &a})
	a0 := a.Load()
	if _, err := st.AddTask("written by the TUI's connection", "", "normal", store.TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	tick(s)
	if got := a.Load() - a0; got != 0 {
		t.Fatalf("the TUI's own write caused %d reread(s)", got)
	}
	if !strings.Contains(rendered(s), "screen A") {
		t.Fatal("screen gone")
	}
}

// A person answering a prompt about a row must not have the rows move under
// them: the reread waits until the screen lets go of the keyboard.
func TestARereadWaitsWhileTheScreenHoldsTheKeyboard(t *testing.T) {
	st := openTestStore(t)
	s := liveSession(t, st, tasksScreen{})
	other := otherConnection(t, st)
	if _, err := other.AddTask("first", "", "normal", store.TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	tick(s)
	shows(t, s, "1 of 1 tasks")

	s.Keys("/") // searching: the screen has the keyboard
	id, err := other.AddTask("second", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.StartSession(&id, nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	tick(s)
	shows(t, s, "1 of 1 tasks", "session #1") // the header still follows
	lacks(t, s, "second")

	s.Keys("esc")
	tick(s)
	shows(t, s, "second", "2 of 2 tasks")
}
