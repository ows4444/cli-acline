package orchestrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"acline/internal/scaffold"
	"acline/internal/store"
)

func agentTools(t *testing.T, root, role string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".claude", "agents", role+".md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^tools: (.*)$`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatalf("agents/%s.md has no tools line", role)
	}
	var out []string
	for _, s := range strings.Split(m[1], ",") {
		out = append(out, strings.TrimSpace(s))
	}
	return out
}

func canEdit(tools []string) bool { return contains(tools, "Edit") || contains(tools, "Write") }

// What a role's Claude Code agent file may do and what the orchestrator lets the
// same role's step do are separate lists. They must agree on the one thing that
// matters most: which roles may change files.
func TestAgentFilesAndOrchestratorToolListsAgreeOnWhoMayEdit(t *testing.T) {
	root := t.TempDir()
	if _, err := scaffold.Write(root); err != nil {
		t.Fatal(err)
	}
	for role, action := range map[string]string{
		"developer": store.RouteStartWork,
		"qa":        store.RouteVerify,
		"security":  store.RouteVerify,
		"architect": store.RouteDefineCriteria,
	} {
		allow, _ := Tools(action, nil)
		agent := agentTools(t, root, role)
		if canEdit(agent) != canEdit(allow) {
			t.Errorf("%s: the agent file says edit=%t but the orchestrator's %s step says edit=%t", role, canEdit(agent), action, canEdit(allow))
		}
		usesBash := false
		for _, a := range allow {
			usesBash = usesBash || strings.HasPrefix(a, "Bash(")
		}
		if usesBash && !contains(agent, "Bash") {
			t.Errorf("%s: the %s step runs shell commands but the agent file has no Bash tool", role, action)
		}
	}
	// The planning run is the architect's, read-only.
	planAllow, _ := PlanTools(nil)
	if canEdit(planAllow) || canEdit(agentTools(t, root, "architect")) {
		t.Error("the architect's planning run and agent file must both be read-only")
	}
	// Only the developer's file may edit.
	for _, role := range []string{"designer", "qa", "security", "architect"} {
		if canEdit(agentTools(t, root, role)) {
			t.Errorf("%s's agent file allows editing files", role)
		}
	}
	if !canEdit(agentTools(t, root, "developer")) {
		t.Error("the developer's agent file cannot edit files")
	}
}
