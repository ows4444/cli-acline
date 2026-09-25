package store

import (
	"errors"
	"testing"
)

func TestCheckRunnerIsProjectScopedAndHumanOnly(t *testing.T) {
	h := humanStore(t)
	pid, err := h.AddProject("web", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SetCheckRunner(pid, "sast", "semgrep --error .", ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.CheckRunnerCommand(&pid, "sast"); got != "semgrep --error ." {
		t.Errorf("command = %q", got)
	}
	if got, _ := h.CheckRunnerCommand(&pid, "sca"); got != "" {
		t.Errorf("unset kind = %q", got)
	}
	if got, _ := h.CheckRunnerCommand(nil, "sast"); got != "" {
		t.Errorf("no project = %q", got)
	}
	if err := h.SetCheckRunner(pid, "sast", "semgrep --strict .", ""); err != nil { // replace
		t.Fatal(err)
	}
	if rs, _ := h.ListCheckRunners(pid); len(rs) != 1 || rs[0].Command != "semgrep --strict ." {
		t.Errorf("list = %+v", rs)
	}
	if h.SetCheckRunner(pid, "human_review", "x", "") == nil || h.SetCheckRunner(pid, "test", "  ", "") == nil {
		t.Error("invalid kind/empty command accepted")
	}
	if err := h.UnsetCheckRunner(pid, "sast", ""); err != nil {
		t.Fatal(err)
	}
	if h.UnsetCheckRunner(pid, "sast", "") == nil {
		t.Error("unsetting a missing runner should error")
	}
}

func TestAgentCannotConfigureCheckRunner(t *testing.T) {
	a := agentStore(t)
	pid, _ := a.AddProject("web", "", "")
	if err := a.SetCheckRunner(pid, "test", "true", ""); !errors.Is(err, ErrAgentCannotConfigureRunner) {
		t.Fatalf("agent set = %v", err)
	}
	if err := a.UnsetCheckRunner(pid, "test", ""); !errors.Is(err, ErrAgentCannotConfigureRunner) {
		t.Fatalf("agent unset = %v", err)
	}
}
