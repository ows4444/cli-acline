package store

import (
	"errors"
	"strings"
	"testing"
)

func mkTask(t *testing.T, s *Store, title string) int64 {
	t.Helper()
	id, err := s.AddTask(title, "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.AddCriterion(id, "When X, the system shall Y")
	return id
}

func TestTaskWaitsForItsPrerequisitesThenProceeds(t *testing.T) {
	s := humanStore(t)
	api := mkTask(t, s, "build the API")
	ui := mkTask(t, s, "build the UI")
	if _, err := s.AddLink(ui, api, "depends_on"); err != nil {
		t.Fatal(err)
	}
	r := routeOf(t, s, ui)
	if r.Action != RouteWaitDependency || r.NeedsHuman || len(r.WaitingOn) != 1 || !strings.Contains(r.WaitingOn[0], "#1 build the API [todo]") {
		t.Fatalf("route = %+v", r)
	}
	// The prerequisite itself is unaffected.
	if r := routeOf(t, s, api); r.Action != RouteStartWork {
		t.Fatalf("prerequisite route = %+v", r)
	}
	s.AddCheck(api, "test", "pass", "")
	if _, err := s.CompleteTask(api, false, ""); err != nil {
		t.Fatal(err)
	}
	if r := routeOf(t, s, ui); r.Action != RouteStartWork || len(r.WaitingOn) != 0 {
		t.Fatalf("after the prerequisite is done: %+v", r)
	}
}

func TestBlocksIsTheSameOrderingFromTheOtherSide(t *testing.T) {
	s := humanStore(t)
	first := mkTask(t, s, "first")
	second := mkTask(t, s, "second")
	s.AddLink(first, second, "blocks") // first blocks second: second waits
	if r := routeOf(t, s, second); r.Action != RouteWaitDependency {
		t.Fatalf("second = %+v", r)
	}
	if r := routeOf(t, s, first); r.Action != RouteStartWork {
		t.Fatalf("first = %+v", r)
	}
	// "related" orders nothing.
	third := mkTask(t, s, "third")
	s.AddLink(third, first, "related")
	if r := routeOf(t, s, third); r.Action != RouteStartWork {
		t.Fatalf("related link made it wait: %+v", r)
	}
}

func TestCancelledPrerequisiteStillBlocksAndSaysSo(t *testing.T) {
	s := humanStore(t)
	a := mkTask(t, s, "a")
	b := mkTask(t, s, "b")
	s.AddLink(b, a, "depends_on")
	updateTaskStatus(s.DB, a, "cancelled")
	r := routeOf(t, s, b)
	if r.Action != RouteWaitDependency || !strings.Contains(r.WaitingOn[0], "[cancelled]") {
		t.Fatalf("route = %+v", r)
	}
}

func TestNextTaskSkipsWaitingTasksButReportsThemWhenNothingElseIsOpen(t *testing.T) {
	s := humanStore(t)
	dep := mkTask(t, s, "dependency")
	urgent, _ := s.AddTask("urgent but waiting", "", "urgent", TaskOpts{})
	s.AddCriterion(urgent, "When X, the system shall Y")
	s.AddLink(urgent, dep, "depends_on")
	if r, _ := s.NextTask(nil); r == nil || r.TaskID != dep {
		t.Fatalf("want the dependency #%d ahead of the urgent waiting task, got %+v", dep, r)
	}
	updateTaskStatus(s.DB, dep, "cancelled") // it will never finish, and nothing else is open
	// dep is cancelled (not open); the urgent one waits on it -> reported, not dropped
	if r, _ := s.NextTask(nil); r == nil || r.TaskID != urgent || r.Action != RouteWaitDependency {
		t.Fatalf("fallback = %+v", r)
	}
}

func TestGateWarnsAboutAnUnfinishedPrerequisiteButDoesNotBlock(t *testing.T) {
	s := humanStore(t)
	a := mkTask(t, s, "prerequisite")
	b := mkTask(t, s, "dependent")
	s.AddLink(b, a, "depends_on")
	s.AddCheck(b, "test", "pass", "")
	g, _ := s.EvaluateGate(b)
	if !g.OK() {
		t.Fatalf("a prerequisite must not block the gate: %+v", g)
	}
	found := false
	for _, w := range g.Warnings {
		found = found || strings.Contains(w, "prerequisite not done: #1 prerequisite")
	}
	if !found {
		t.Fatalf("no warning: %+v", g.Warnings)
	}
}

func TestLinksRejectSelfAndCycles(t *testing.T) {
	s := humanStore(t)
	a, b, c := mkTask(t, s, "a"), mkTask(t, s, "b"), mkTask(t, s, "c")
	if _, err := s.AddLink(a, a, "related"); err == nil {
		t.Error("self link accepted")
	}
	s.AddLink(a, b, "depends_on") // b before a
	s.AddLink(b, c, "depends_on") // c before b, so c -> b -> a
	if _, err := s.AddLink(a, b, "blocks"); !errors.Is(err, ErrLinkCycle) {
		t.Errorf("a blocks b (a before b) contradicts b before a: %v", err)
	}
	if _, err := s.AddLink(c, a, "depends_on"); !errors.Is(err, ErrLinkCycle) {
		t.Errorf("c depends_on a closes c->b->a->c: %v", err)
	}
	if _, err := s.AddLink(c, a, "blocks"); err != nil { // c before a: consistent
		t.Errorf("a consistent link was refused: %v", err)
	}
	if _, err := s.AddLink(c, a, "related"); err != nil { // related never cycles
		t.Errorf("related refused: %v", err)
	}
}
