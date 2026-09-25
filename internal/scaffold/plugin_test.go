package scaffold

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportPluginCarriesSkillsAndAgentsButNoSafetySettings(t *testing.T) {
	dir := t.TempDir()
	files, err := ExportPlugin(dir, PluginOptions{Version: "v1.2.3", WithMCP: true})
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	data, err := os.ReadFile(filepath.Join(dir, ".claude-plugin", "plugin.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if manifest["name"] != "acline" || manifest["version"] != "1.2.3" {
		t.Fatalf("manifest = %v", manifest)
	}
	for _, want := range []string{"skills/work/SKILL.md", "agents/security.md", ".mcp.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	// No hooks or settings: a plugin can't carry deny rules or the agent env, and
	// the guard must never run without them.
	for _, absent := range []string{"hooks/hooks.json", "settings.json"} {
		if _, err := os.Stat(filepath.Join(dir, absent)); err == nil {
			t.Errorf("%s must not be in the plugin", absent)
		}
	}
	var mcp struct {
		MCPServers map[string]struct {
			Args []string          `json:"args"`
			Env  map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	data, _ = os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err := json.Unmarshal(data, &mcp); err != nil {
		t.Fatal(err)
	}
	if s := mcp.MCPServers["acline"]; s.Env["ACLINE_ACTOR_TYPE"] != "agent" || len(s.Args) < 4 || s.Args[3] != "agent" {
		t.Fatalf("the plugin's MCP server must run as an agent: %+v", s)
	}
	if len(files) < 10 {
		t.Fatalf("only %d files written", len(files))
	}

	if _, err := ExportPlugin(dir, PluginOptions{}); !errors.Is(err, ErrPluginDirNotEmpty) {
		t.Fatalf("a non-empty directory without Force: err = %v", err)
	}
	if _, err := ExportPlugin(dir, PluginOptions{Force: true}); err != nil {
		t.Fatalf("Force: %v", err)
	}
}
