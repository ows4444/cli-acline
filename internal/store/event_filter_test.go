package store

import "testing"

func TestQueryEventsFiltersByTypeAndListsTheTypes(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	if _, _, err := s.AddCriterion(id, "When x, the system shall y"); err != nil {
		t.Fatal(err)
	}
	events, err := s.QueryEvents(EventFilter{Type: "task_created", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "task_created" || events[0].TaskID.Int64 != id {
		t.Fatalf("events = %+v", events)
	}
	types, err := s.EventTypes()
	if err != nil {
		t.Fatal(err)
	}
	has := map[string]bool{}
	for _, ty := range types {
		has[ty] = true
	}
	if !has["task_created"] || !has["criterion_added"] {
		t.Errorf("types = %v", types)
	}
}
