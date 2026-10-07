package app

import (
	"errors"
	"testing"

	"acline/internal/store"
)

func humanTestStore(t *testing.T) *store.Store {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "human", ID: "owner"}
	return st
}

func TestDeferTaskNeedsAReasonUnlessClearing(t *testing.T) {
	st := humanTestStore(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	if err := DeferTask(st, id, false, " ", ""); !errors.Is(err, ErrDeferReasonRequired) {
		t.Fatalf("err = %v, want ErrDeferReasonRequired", err)
	}
	if err := DeferTask(st, id, false, "after v2", "v2 ships"); err != nil {
		t.Fatal(err)
	}
	if task, _ := st.GetTask(id); !task.Deferred {
		t.Error("task not deferred")
	}
	if err := DeferTask(st, id, true, "", ""); err != nil {
		t.Fatal(err)
	}
	if task, _ := st.GetTask(id); task.Deferred {
		t.Error("deferred flag not cleared")
	}
}

func TestLinkTasksNeedsBothTasks(t *testing.T) {
	st := humanTestStore(t)
	a, _ := st.AddTask("a", "", "normal", store.TaskOpts{})
	b, _ := st.AddTask("b", "", "normal", store.TaskOpts{})
	if _, err := LinkTasks(st, a, "depends_on", 99); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing other: err = %v, want ErrNotFound", err)
	}
	if _, err := LinkTasks(st, a, "depends_on", b); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkTasks(st, b, "depends_on", a); !errors.Is(err, store.ErrLinkCycle) {
		t.Fatalf("cycle: err = %v, want ErrLinkCycle", err)
	}
}

func TestAddCriterionNeedsText(t *testing.T) {
	st := humanTestStore(t)
	id, _ := st.AddTask("t", "", "normal", store.TaskOpts{})
	if _, _, err := AddCriterion(st, id, "  "); !errors.Is(err, ErrCriterionTextRequired) {
		t.Fatalf("err = %v, want ErrCriterionTextRequired", err)
	}
	if _, pattern, err := AddCriterion(st, id, "When saved, the system shall confirm"); err != nil || pattern == "" {
		t.Fatalf("EARS criterion: pattern %q, err %v", pattern, err)
	}
}

// An invalid target must not leave the status changed.
func TestUpdateMilestoneIsAllOrNothing(t *testing.T) {
	st := humanTestStore(t)
	id, err := st.AddMilestone("v1", store.MilestoneOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateMilestone(st, id, "", ""); !errors.Is(err, ErrNothingToUpdate) {
		t.Errorf("empty: err = %v, want ErrNothingToUpdate", err)
	}
	before, _ := st.GetMilestone(id)
	if err := UpdateMilestone(st, id, "active", "next tuesday"); err == nil {
		t.Fatal("expected an invalid target date to be refused")
	}
	if m, _ := st.GetMilestone(id); m.Status != before.Status {
		t.Errorf("status = %q after a refused update, want %q", m.Status, before.Status)
	}
	if err := UpdateMilestone(st, id, "active", "2027-01-01"); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.GetMilestone(id); m.Status != "active" || m.TargetDate.String != "2027-01-01" {
		t.Errorf("milestone = status %q target %q", m.Status, m.TargetDate.String)
	}
}

func TestAddRoleNeedsAProject(t *testing.T) {
	st := humanTestStore(t)
	if _, err := AddRole(st, AddRoleRequest{Name: "reviewer", Kind: "both"}); !errors.Is(err, ErrRoleNeedsProject) {
		t.Fatalf("err = %v, want ErrRoleNeedsProject", err)
	}
	if _, err := st.AddProject("demo", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := AddRole(st, AddRoleRequest{Name: "reviewer", Kind: "both", ProjectArg: "demo"}); err != nil {
		t.Fatal(err)
	}
}

func TestAddFeatureNeedsAName(t *testing.T) {
	st := humanTestStore(t)
	if _, err := AddFeature(st, AddFeatureRequest{Name: " "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("err = %v, want ErrNameRequired", err)
	}
	if _, err := AddFeature(st, AddFeatureRequest{Name: "export"}); err != nil {
		t.Fatal(err)
	}
}

// From another project's folder, an eval for a task belongs to the task's
// project, which is the only one its autonomy promotion counts.
func TestRecordEvalTakesTheTasksProject(t *testing.T) {
	st := humanTestStore(t)
	dirA, dirB := t.TempDir(), t.TempDir()
	a, _ := st.AddProject("a", dirA, "")
	if _, err := st.AddProject("b", dirB, ""); err != nil {
		t.Fatal(err)
	}
	task, _ := st.AddTask("t", "", "normal", store.TaskOpts{ProjectID: &a})
	t.Chdir(dirB)

	if _, err := RecordEval(st, RecordEvalRequest{TaskID: &task, Suite: "unit", PassRate: 0.95, AllowCwdFallback: true}); err != nil {
		t.Fatal(err)
	}
	evals, _ := st.ListEvals(&a, "unit", 10)
	if len(evals) != 1 {
		t.Fatalf("project a's evals = %d, want 1", len(evals))
	}
	missing := int64(99)
	if _, err := RecordEval(st, RecordEvalRequest{TaskID: &missing, Suite: "unit", PassRate: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing task: err = %v, want ErrNotFound", err)
	}
}
