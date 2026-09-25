package store

import (
	"os"
	"testing"
)

// TestTaskFieldUpdaters covers the small task.go setters that previously had no direct test: UpdateTaskPriority,
// UpdateTaskArea, UpdateTaskType, UpdateTaskRisk, LinkTaskDecision,
// LinkTaskSpec, SetCriterionDone.
func TestTaskFieldUpdaters(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddTask("task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdateTaskPriority(id, "urgent"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTaskArea(id, "backend"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTaskType(id, "bug"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTaskRisk(id, "high"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetTask(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Priority != "urgent" || got.Area.String != "backend" || got.Type.String != "bug" || got.Risk != "high" {
		t.Errorf("unexpected task after updates: %+v", got)
	}

	if err := s.UpdateTaskPriority(id, "nonsense"); err == nil {
		t.Error("expected invalid priority to be rejected")
	}
	if err := s.UpdateTaskType(id, "nonsense"); err == nil {
		t.Error("expected invalid type to be rejected")
	}
	if err := s.UpdateTaskRisk(id, "nonsense"); err == nil {
		t.Error("expected invalid risk to be rejected")
	}

	critID, _, err := s.AddCriterion(id, "The system shall do the thing")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCriterionDone(critID, true); err != nil {
		t.Fatal(err)
	}
	criteria, err := s.ListCriteria(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(criteria) != 1 || !criteria[0].Done {
		t.Errorf("expected criterion marked done, got %+v", criteria)
	}

	if err := s.UpdateTaskPriority(999, "high"); err == nil {
		t.Error("expected error updating priority on a non-existent task")
	}
	if err := s.SetCriterionDone(999, true); err == nil {
		t.Error("expected error marking a non-existent criterion done")
	}
}

func TestSetFeatureStatus(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddFeature("dashboard", "live", FeatureOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetFeatureStatus(id, "deprecated"); err != nil {
		t.Fatal(err)
	}
	feats, err := s.ListFeatures("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(feats) != 1 || feats[0].Status != "deprecated" {
		t.Errorf("expected status=deprecated, got %+v", feats)
	}
	if err := s.SetFeatureStatus(id, "nonsense"); err == nil {
		t.Error("expected invalid feature status to be rejected")
	}
}

func TestVerifyDependency(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddDependency(nil, nil, "npm", "left-pad", "1.3.0", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyDependency(id); err != nil {
		t.Fatal(err)
	}
	deps, err := s.ListDependencies(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || !deps[0].Verified {
		t.Errorf("expected dependency verified, got %+v", deps)
	}
	if err := s.VerifyDependency(999); err == nil {
		t.Error("expected error verifying a non-existent dependency")
	}
}

func TestTasksForSpec(t *testing.T) {
	s := humanStore(t)
	specID, err := s.AddSpec("a spec", "body")
	if err != nil {
		t.Fatal(err)
	}
	otherSpecID, err := s.AddSpec("other spec", "body")
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := s.AddTask("derived task", "", "normal", TaskOpts{SpecID: &specID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddTask("unrelated task", "", "normal", TaskOpts{SpecID: &otherSpecID}); err != nil {
		t.Fatal(err)
	}

	tasks, err := s.TasksForSpec(specID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != taskID {
		t.Errorf("expected only the task derived from this spec, got %+v", tasks)
	}
}

func TestLogTaskEvent(t *testing.T) {
	s := humanStore(t)
	taskID, err := s.AddTask("task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.LogTaskEvent(taskID, "note", "something happened")

	events, err := s.ListEvents(&taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Message != "something happened" {
		t.Fatalf("expected the logged event to be recorded, got %+v", events)
	}
}

func TestDefaultPath(t *testing.T) {
	t.Run("honors ACLINE_DB", func(t *testing.T) {
		t.Setenv("ACLINE_DB", "/custom/path/store.db")
		p, err := DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		if p != "/custom/path/store.db" {
			t.Errorf("DefaultPath() = %q, want ACLINE_DB override", p)
		}
	})

	t.Run("honors XDG_DATA_HOME", func(t *testing.T) {
		os.Unsetenv("ACLINE_DB")
		t.Setenv("ACLINE_DB", "")
		t.Setenv("XDG_DATA_HOME", "/xdg/data")
		p, err := DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		if p != "/xdg/data/acline/store.db" {
			t.Errorf("DefaultPath() = %q, want XDG_DATA_HOME-based path", p)
		}
	})

	t.Run("falls back to home dir", func(t *testing.T) {
		t.Setenv("ACLINE_DB", "")
		t.Setenv("XDG_DATA_HOME", "")
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home dir available in this environment")
		}
		p, err := DefaultPath()
		if err != nil {
			t.Fatal(err)
		}
		want := home + "/.acline/store.db"
		if p != want {
			t.Errorf("DefaultPath() = %q, want %q", p, want)
		}
	})
}
