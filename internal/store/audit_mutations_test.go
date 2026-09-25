package store

import "testing"

// These task mutations wrote no audit event, so the trail could not say who
// checked off a criterion or re-prioritised a task.
func TestTaskMutationsAreAudited(t *testing.T) {
	h := humanStore(t)
	id, _ := h.AddTask("t", "", "normal", TaskOpts{})
	other, _ := h.AddTask("u", "", "normal", TaskOpts{})
	pid, _ := h.AddProject("p", "", "")

	crit, _, err := h.AddCriterion(id, "When X, the system shall Y")
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		name      string
		run       func() error
		eventType string
		task      *int64
	}{
		{"criterion added", func() error { return nil }, "criterion_added", &id},
		{"criterion checked", func() error { return h.SetCriterionDone(crit, true) }, "criterion_checked", &id},
		{"priority", func() error { return h.UpdateTaskPriority(id, "high") }, "task_updated", &id},
		{"area", func() error { return h.UpdateTaskArea(id, "store") }, "task_updated", &id},
		{"type", func() error { return h.UpdateTaskType(id, "bug") }, "task_updated", &id},
		{"deferred", func() error { return h.DeferTask(id, true, "later", "") }, "task_deferred", &id},
		{"link", func() error { _, err := h.AddLink(id, other, "related"); return err }, "link_added", &id},
		{"runner", func() error { return h.SetCheckRunner(pid, "test", "go test ./...", "") }, "check_runner_set", nil},
	} {
		before, _ := h.LatestEventID()
		if step.name != "criterion added" {
			if err := step.run(); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
		} else {
			before = 0
		}
		events, _ := h.QueryEvents(EventFilter{Limit: 50})
		found := false
		for _, e := range events {
			if e.ID > before && e.Type == step.eventType && (step.task == nil || (e.TaskID.Valid && e.TaskID.Int64 == *step.task)) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no %s event: %+v", step.name, step.eventType, events)
		}
	}
	if err := h.SetCriterionDone(9999, true); err == nil {
		t.Fatal("checking off a missing criterion succeeded")
	}
}
