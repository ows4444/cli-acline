package cmd

import (
	"testing"

	"acline/internal/orchestrate"
	"acline/internal/scaffold"
)

// What `acline init` ships must satisfy the orchestrator's own launch check, or
// a freshly initialised project could never be orchestrated (and the two lists
// of guarded tools could silently drift apart).
func TestScaffoldedProjectPassesTheOrchestratorHookPreflight(t *testing.T) {
	dir := t.TempDir()
	if _, err := scaffold.Write(dir); err != nil {
		t.Fatal(err)
	}
	var tools []string
	for name := range requiredGuardTools() {
		tools = append(tools, name)
	}
	if err := orchestrate.PreflightHooks(dir, tools); err != nil {
		t.Fatalf("a freshly scaffolded project is refused: %v", err)
	}
}

// Without --project, `orchestrate step|run` picked the next task from every
// project in the shared store. It now scopes to the project the working
// directory belongs to, as commands that act do.
func TestOrchestrateScopesToTheCurrentProject(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	a, b := t.TempDir(), t.TempDir()
	aid, err := s.AddProject("a", a, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProject("b", b, ""); err != nil {
		t.Fatal(err)
	}
	t.Chdir(a)
	o, err := (&orchFlags{c: c}).options(nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if o.ProjectID == nil || *o.ProjectID != aid {
		t.Fatalf("project scope = %v, want project a (#%d)", o.ProjectID, aid)
	}
}
