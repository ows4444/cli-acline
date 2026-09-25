package store

import "testing"

// TestMilestoneLifecycle is a regression test: the milestone feature (milestone.go) had no direct unit test at all
// -- AddMilestone/GetMilestone/ListMilestones/SetMilestoneStatus/
// SetMilestoneTarget/SetTaskMilestone/MilestoneTasks/GetMilestoneProgress
// were only ever exercised indirectly (if at all) through the cmd layer's
// integration tests, which don't count toward this package's own coverage.
func TestMilestoneLifecycle(t *testing.T) {
	s := humanStore(t)

	id, err := s.AddMilestone("v1 launch", MilestoneOpts{Description: "first GA release", TargetDate: "2026-03-01"})
	if err != nil {
		t.Fatal(err)
	}

	m, err := s.GetMilestone(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "v1 launch" || m.Status != "planned" || m.Description.String != "first GA release" || m.TargetDate.String != "2026-03-01" {
		t.Errorf("unexpected milestone after add: %+v", m)
	}

	if err := s.SetMilestoneStatus(id, "active"); err != nil {
		t.Fatal(err)
	}
	m, err = s.GetMilestone(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "active" {
		t.Errorf("expected status=active, got %q", m.Status)
	}

	if err := s.SetMilestoneTarget(id, "2026-06-15"); err != nil {
		t.Fatal(err)
	}
	m, err = s.GetMilestone(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.TargetDate.String != "2026-06-15" {
		t.Errorf("expected updated target date, got %q", m.TargetDate.String)
	}

	taskID, err := s.AddTask("shippable task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTaskMilestone(taskID, &id); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.MilestoneTasks(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("expected the assigned task in MilestoneTasks, got %+v", tasks)
	}

	progress, err := s.GetMilestoneProgress(id)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Total != 1 || progress.Done != 0 {
		t.Errorf("unexpected progress before completion: %+v", progress)
	}

	if err := updateTaskStatus(s.DB, taskID, "done"); err != nil {
		t.Fatal(err)
	}
	progress, err = s.GetMilestoneProgress(id)
	if err != nil {
		t.Fatal(err)
	}
	if progress.Total != 1 || progress.Done != 1 {
		t.Errorf("expected 1/1 done after completion, got %+v", progress)
	}

	// Clearing the milestone (nil) removes the task from MilestoneTasks.
	if err := s.SetTaskMilestone(taskID, nil); err != nil {
		t.Fatal(err)
	}
	tasks, err = s.MilestoneTasks(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected no tasks after clearing the milestone assignment, got %+v", tasks)
	}
}

func TestListMilestonesFiltersByStatusAndProject(t *testing.T) {
	s := humanStore(t)
	projID, err := s.AddProject("demo", "/tmp/demo", "hotl")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.AddMilestone("unscoped planned", MilestoneOpts{}); err != nil {
		t.Fatal(err)
	}
	scopedID, err := s.AddMilestone("scoped planned", MilestoneOpts{ProjectID: &projID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneStatus(scopedID, "done"); err != nil {
		t.Fatal(err)
	}

	all, err := s.ListMilestones("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 milestones unfiltered, got %d", len(all))
	}

	planned, err := s.ListMilestones("planned", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) != 1 || planned[0].Name != "unscoped planned" {
		t.Fatalf("expected only the planned milestone, got %+v", planned)
	}

	scoped, err := s.ListMilestones("", &projID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].ID != scopedID {
		t.Fatalf("expected only the project-scoped milestone, got %+v", scoped)
	}
}

func TestMilestoneRejectsInvalidStatusAndDate(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddMilestone("m", MilestoneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMilestoneStatus(id, "nonsense"); err == nil {
		t.Error("expected an invalid milestone status to be rejected")
	}
	if err := s.SetMilestoneTarget(id, "not-a-date"); err == nil {
		t.Error("expected an invalid target date to be rejected")
	}
	if _, err := s.AddMilestone("bad date", MilestoneOpts{TargetDate: "06/15/2026"}); err == nil {
		t.Error("expected AddMilestone to reject a non-YYYY-MM-DD target date")
	}
}

func TestMilestoneOperationsOnMissingIDsError(t *testing.T) {
	s := humanStore(t)
	if _, err := s.GetMilestone(999); err == nil {
		t.Error("expected error getting a non-existent milestone")
	}
	if err := s.SetMilestoneStatus(999, "active"); err == nil {
		t.Error("expected error setting status on a non-existent milestone")
	}
	if err := s.SetMilestoneTarget(999, "2026-01-01"); err == nil {
		t.Error("expected error setting target on a non-existent milestone")
	}
}
