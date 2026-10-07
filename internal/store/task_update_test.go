package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestUpdateTaskAppliesEveryFieldWithItsEvent(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	ms, err := s.AddMilestone("m1", MilestoneOpts{})
	if err != nil {
		t.Fatal(err)
	}

	changed, err := s.UpdateTask(id, TaskUpdate{
		Status: "blocked", Reason: "waiting", Priority: "high", Area: "cli", Type: "bug",
		Risk: "high", Autonomy: "hitl", Milestone: &ms,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	want := []string{"status=blocked", "priority=high", "area=cli", "type=bug", "risk=high", "autonomy=hitl", "milestone=#1"}
	if !reflect.DeepEqual(changed, want) {
		t.Errorf("changed = %v, want %v", changed, want)
	}
	task, _ := s.GetTask(id)
	if task.Status != "blocked" || task.BlockedReason.String != "waiting" || task.Priority != "high" ||
		task.Area.String != "cli" || task.Type.String != "bug" || task.Risk != "high" ||
		task.Autonomy != "hitl" || task.MilestoneID.Int64 != ms {
		t.Errorf("task = %+v", task)
	}
	events, _ := s.ListEvents(&id, 0)
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	for _, typ := range []string{"status_change", "task_updated", "risk_changed", "autonomy_changed", "milestone_change"} {
		found := false
		for _, got := range types {
			found = found || got == typ
		}
		if !found {
			t.Errorf("no %s event in %v", typ, types)
		}
	}
}

// A refused field must not leave the fields before it applied (plan 4.4).
func TestUpdateTaskIsAllOrNothing(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "high"})
	before, _ := s.ListEvents(&id, 0)
	missing := int64(999)

	s.Actor = Actor{Type: "agent", ID: "bot"}
	for name, u := range map[string]TaskUpdate{
		"refused risk":      {Status: "in_progress", Priority: "urgent", Risk: "low"},
		"missing milestone": {Status: "in_progress", Priority: "urgent", Milestone: &missing},
		"invalid type":      {Status: "in_progress", Priority: "urgent", Type: "nope"},
		"done":              {Status: "done", Priority: "urgent"},
	} {
		if _, err := s.UpdateTask(id, u); err == nil {
			t.Errorf("%s: expected an error", name)
		}
		task, _ := s.GetTask(id)
		if task.Status != "todo" || task.Priority != "normal" || task.Risk != "high" {
			t.Errorf("%s: task changed to status %q priority %q risk %q", name, task.Status, task.Priority, task.Risk)
		}
	}
	if after, _ := s.ListEvents(&id, 0); len(after) != len(before) {
		t.Errorf("events %d -> %d, want no new events", len(before), len(after))
	}
}

func TestUpdateTaskLetsAnAgentTighten(t *testing.T) {
	s := agentStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{})
	changed, err := s.UpdateTask(id, TaskUpdate{Risk: "critical", Autonomy: "hitl"})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %v", changed)
	}
	if _, err := s.UpdateTask(id, TaskUpdate{Risk: "low"}); !errors.Is(err, ErrAgentCannotLoosenTask) {
		t.Errorf("lowering: err = %v, want ErrAgentCannotLoosenTask", err)
	}
}

func TestUpdateTaskSkipsALevelAlreadyAtItsValue(t *testing.T) {
	s := humanStore(t)
	id, _ := s.AddTask("t", "", "normal", TaskOpts{Risk: "medium"})
	changed, err := s.UpdateTask(id, TaskUpdate{Risk: "medium", Priority: "low"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(changed, []string{"priority=low"}) {
		t.Errorf("changed = %v, want only priority", changed)
	}
}
