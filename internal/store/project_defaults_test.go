package store

import (
	"errors"
	"testing"
)

// `acline init` asked "default new tasks to hitl?" and "default risk tier?",
// stored the first as projects.autonomy_default and the second nowhere, and
// no task ever used either. A task added to a project without its own risk or
// autonomy now takes the project's defaults.
func TestTasksTakeTheirProjectsDefaults(t *testing.T) {
	h := humanStore(t)
	p, err := h.AddProjectWithDefaults("strict", "", "hitl", "medium", "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := h.AddTask("t", "", "normal", TaskOpts{ProjectID: &p})
	got, _ := h.GetTask(id)
	if got.Autonomy != "hitl" || got.Risk != "medium" {
		t.Fatalf("task in a hitl/medium project = %s/%s", got.Autonomy, got.Risk)
	}
	id, _ = h.AddTask("t", "", "normal", TaskOpts{ProjectID: &p, Autonomy: "hotl", Risk: "high"})
	if got, _ := h.GetTask(id); got.Autonomy != "hotl" || got.Risk != "high" {
		t.Fatalf("explicit values lost to the defaults: %s/%s", got.Autonomy, got.Risk)
	}
	id, _ = h.AddTask("t", "", "normal", TaskOpts{})
	if got, _ := h.GetTask(id); got.Autonomy != "hotl" || got.Risk != "low" {
		t.Fatalf("a task with no project = %s/%s, want hotl/low", got.Autonomy, got.Risk)
	}
	// An agent's task in the project gets the defaults too.
	id, err = asAgent(h).AddTask("t", "", "normal", TaskOpts{ProjectID: &p})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := h.GetTask(id); got.Autonomy != "hitl" {
		t.Fatalf("agent's task autonomy = %s", got.Autonomy)
	}
}

// auto is earned from a measured eval, task by task; a project default would
// hand it to every new task without one.
func TestAProjectCannotDefaultToAuto(t *testing.T) {
	h := humanStore(t)
	if _, err := h.AddProjectWithDefaults("loose", "", "auto", "", ""); !errors.Is(err, ErrProjectDefaultAuto) {
		t.Fatalf("project default auto = %v, want ErrProjectDefaultAuto", err)
	}
	if _, err := h.AddProjectWithDefaults("bad", "", "", "extreme", ""); err == nil {
		t.Fatal("an invalid default risk was accepted")
	}
}
