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
