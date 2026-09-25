package scaffold

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWriteCreatesCompleteClaudeScaffold(t *testing.T) {
	root := t.TempDir()
	result, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Created) == 0 {
		t.Fatal("expected scaffold files to be created")
	}
	for _, rel := range []string{
		"settings.json",
		"skills/recall/SKILL.md",
		"skills/reflect/SKILL.md",
		"skills/spec/SKILL.md",
		"skills/work/SKILL.md",
		"skills/ship/SKILL.md",
		"skills/status/SKILL.md",
		"skills/review/SKILL.md",
		"skills/bugfix/SKILL.md",
		"skills/dep-add/SKILL.md",
		"vault/SOUL.md",
		"vault/USER.md",
		"vault/BOOTSTRAP.md",
		"vault/.gitignore",
	} {
		if _, err := os.Stat(filepath.Join(root, ".claude", filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s: %v", rel, err)
		}
	}
	// The hooks are `acline hook <event>` commands in settings.json, not files.
	for _, gone := range []string{"vault/daily", "vault/research"} {
		if _, err := os.Stat(filepath.Join(root, ".claude", filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s is not scaffolded any more: nothing in acline reads it", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".claude", "hooks")); err == nil {
		t.Error("a .claude/hooks directory should not be scaffolded any more (no Python runtime dependency)")
	}
}

func TestWriteIsIdempotentAndPreservesExistingFiles(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}
	soul := filepath.Join(root, ".claude", "vault", "SOUL.md")
	if err := os.WriteFile(soul, []byte("custom identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(soul)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "custom identity\n" {
		t.Fatalf("existing vault file was overwritten: %q", got)
	}
	if len(result.Created) != 0 || len(result.Merged) != 0 {
		t.Fatalf("second scaffold should be a no-op: %+v", result)
	}
}

func TestWriteMergesSettingsWithoutDroppingExistingConfiguration(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"permissions":{"allow":["Read"]},"hooks":{"PreToolUse":[{"matcher":"Custom","hooks":[]}]}}`
	settings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settings, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Merged) != 1 {
		t.Fatalf("expected settings merge, got %+v", result)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed["permissions"]; !ok {
		t.Fatal("existing permissions were dropped")
	}
	hooks := parsed["hooks"].(map[string]any)
	if len(hooks["PreToolUse"].([]any)) != 2 {
		t.Fatalf("expected custom and ACLine PreToolUse entries, got %#v", hooks["PreToolUse"])
	}
}

// TestUpgradeOverwritesChangedHooksAndSkillsButNotVault is a regression
// test: before Options.Upgrade existed, a
// bug fix to a bundled hook/skill script shipped with a new acline build
// never reached an already-scaffolded project, since Write only ever
// created missing files. This confirms --upgrade brings a stale hook/skill
// file in line with what this build ships, while still never touching
// vault/ (a one-time-seeded template, not kept in lockstep) or a file that
// already matches the shipped version.
func TestUpgradeOverwritesChangedSkillsButNotVault(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}

	skill := filepath.Join(root, ".claude", "skills", "recall", "SKILL.md")
	if err := os.WriteFile(skill, []byte("stale skill body"), 0o644); err != nil {
		t.Fatal(err)
	}
	soul := filepath.Join(root, ".claude", "vault", "SOUL.md")
	if err := os.WriteFile(soul, []byte("custom identity\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --upgrade, nothing changes (existing behavior).
	result, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Upgraded) != 0 {
		t.Fatalf("plain Write must never upgrade, got %+v", result.Upgraded)
	}
	if got, _ := os.ReadFile(skill); string(got) != "stale skill body" {
		t.Fatal("plain Write overwrote a skill file")
	}

	result, err = WriteOpts(root, Options{Upgrade: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Upgraded) != 1 {
		t.Fatalf("expected the skill file to be upgraded, got %+v", result.Upgraded)
	}

	shipped, err := assets.ReadFile("assets/skills/recall/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(shipped) {
		t.Fatal("skill was not brought in line with the shipped version")
	}

	// vault/ is a one-time-seeded template: never touched by --upgrade.
	got, err = os.ReadFile(soul)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "custom identity\n" {
		t.Fatalf("--upgrade must never touch vault/, got %q", got)
	}

	// A second --upgrade run against an already-current tree upgrades nothing.
	result, err = WriteOpts(root, Options{Upgrade: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Upgraded) != 0 {
		t.Fatalf("re-running --upgrade against a current tree should upgrade nothing, got %+v", result.Upgraded)
	}
}

func TestWriteSkipsUnparseableSettingsWithoutAbortingScaffold(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(claudeDir, "settings.json")
	// JSONC-style trailing comment: not valid JSON.
	if err := os.WriteFile(settings, []byte("{\n  // custom\n  \"permissions\": {}\n}"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Write(root)
	if err != nil {
		t.Fatalf("a malformed existing settings.json must not abort the whole scaffold: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected a warning about the unparseable settings.json")
	}
	for _, rel := range []string{"skills/reflect/SKILL.md", "vault/SOUL.md", "skills/recall/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(root, ".claude", filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s to still be written: %v", rel, err)
		}
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{\n  // custom\n  \"permissions\": {}\n}" {
		t.Fatal("unparseable settings.json should be left untouched")
	}
}

// TestShippedHookCommandsCallAclineDirectly: the hooks are the acline binary,
// with no Python runtime in the middle. The guard's is `|| exit 2`, so a missing
// binary or a crash blocks the tool call (Claude Code treats exit 2 as a block;
// any other non-zero exit would let it through).
func TestShippedHookCommandsCallAclineDirectly(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}
	commands := hookCommands(t, filepath.Join(root, ".claude", "settings.json"))
	want := map[string]bool{
		"acline hook session-start": true, "acline hook pre-compact": true, "acline hook session-end": true,
		"acline hook stop": true, "acline hook pre-tool-use || exit 2": true, "acline hook post-tool-use": true,
	}
	if len(commands) != len(want) {
		t.Fatalf("expected %d hook commands, got %v", len(want), commands)
	}
	for _, c := range commands {
		if !want[c] {
			t.Errorf("unexpected hook command %q", c)
		}
		if strings.Contains(c, "python") {
			t.Errorf("a Python dependency crept back in: %q", c)
		}
	}
}

func TestWriteReplacesLegacyRelativeHookCommandsAndMergesEnv(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "env": {"ACLINE_ACTOR": "my-agent"},
  "hooks": {
    "SessionStart": [{"hooks": [{"type": "command", "command": "python3 .claude/hooks/session_start.py"}]}],
    "PreCompact": [{"hooks": [{"type": "command", "command": "python3 .claude/hooks/pre_compact.py"}]}],
    "SessionEnd": [{"hooks": [{"type": "command", "command": "python3 .claude/hooks/session_end.py"}]}],
    "PreToolUse": [
      {"matcher": "Read|Edit|Write|Grep|Glob|Bash|NotebookEdit", "hooks": [{"type": "command", "command": "python3 .claude/hooks/pre_tool_use.py"}]},
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "./my-own-hook.sh"}]}
    ]
  }
}`
	settings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settings, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}

	commands := hookCommands(t, settings)
	seen := map[string]int{}
	for _, c := range commands {
		seen[c]++
	}
	if seen["./my-own-hook.sh"] != 1 {
		t.Errorf("user's own hook should be kept exactly once, got %v", commands)
	}
	for _, want := range []string{"acline hook session-start", "acline hook pre-compact", "acline hook session-end", "acline hook pre-tool-use || exit 2"} {
		if seen[want] != 1 {
			t.Errorf("expected %s exactly once, got %d in %v", want, seen[want], commands)
		}
	}
	for _, c := range commands {
		if strings.Contains(c, "python3") {
			t.Errorf("a Python command survived the migration: %q", c)
		}
	}

	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Env["ACLINE_ACTOR"] != "my-agent" {
		t.Errorf("existing env value should win, got %q", parsed.Env["ACLINE_ACTOR"])
	}
	if parsed.Env["ACLINE_ACTOR_TYPE"] != "agent" {
		t.Errorf("missing env key should be added, got %v", parsed.Env)
	}

	result, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Merged) != 0 {
		t.Fatalf("second scaffold after migration should be a no-op: %+v", result)
	}
}

func hookCommands(t *testing.T, settingsPath string) []string {
	t.Helper()
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, entries := range cfg.Hooks {
		for _, e := range entries {
			for _, h := range e.Hooks {
				out = append(out, h.Command)
			}
		}
	}
	return out
}

func TestRoleContractStripsFrontMatterAndRejectsUnknownRoles(t *testing.T) {
	body, ok := RoleContract("developer")
	if !ok || !strings.HasPrefix(body, "# Role: developer") || strings.Contains(body, "protected: true") {
		t.Fatalf("developer contract = %q (ok=%v)", body, ok)
	}
	for _, name := range []string{"", "nope", "../SOUL", "developer/x", "a.b"} {
		if _, ok := RoleContract(name); ok {
			t.Errorf("RoleContract(%q) should not resolve", name)
		}
	}
}

// A project scaffolded by an older acline has an acline PreToolUse entry with the
// old matcher. Re-running init must replace it, not add a second entry that runs
// the guard twice on every tool call.
func TestWriteReplacesAStaleAclineHookInsteadOfDuplicatingIt(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"hooks":{"PreToolUse":[` +
		`{"matcher":"Read|Edit|Write|Grep|Glob|Bash|NotebookEdit","hooks":[{"type":"command","command":"acline hook pre-tool-use || exit 2"}]},` +
		`{"matcher":"Mine","hooks":[{"type":"command","command":"./my-own-hook.sh"}]}]}}`
	settings := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settings, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	var parsed struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	var acline, mine int
	var aclineMatcher string
	for _, e := range parsed.Hooks.PreToolUse {
		for _, h := range e.Hooks {
			switch {
			case strings.Contains(h.Command, "hook pre-tool-use"):
				acline++
				aclineMatcher = e.Matcher
			case strings.Contains(h.Command, "my-own-hook.sh"):
				mine++
			}
		}
	}
	if acline != 1 {
		t.Fatalf("%d acline PreToolUse entries after upgrading, want exactly 1:\n%s", acline, data)
	}
	if mine != 1 {
		t.Errorf("the user's own hook was dropped or duplicated (%d):\n%s", mine, data)
	}
	for _, tool := range []string{"WebFetch", "WebSearch", "Task", "mcp__.*"} {
		if !strings.Contains(aclineMatcher, tool) {
			t.Errorf("upgraded matcher %q does not cover %s", aclineMatcher, tool)
		}
	}
	// running it again changes nothing
	before := string(data)
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(settings); string(after) != before {
		t.Errorf("a second init rewrote the settings:\n%s", after)
	}
}

func TestAgentToolsReadsTheStubsToolList(t *testing.T) {
	dev, ok := AgentTools("developer")
	if !ok || !slices.Contains(dev, "Edit") || !slices.Contains(dev, "Write") {
		t.Fatalf("developer tools = %v, %v", dev, ok)
	}
	for _, role := range []string{"qa", "security", "architect", "designer"} {
		tools, ok := AgentTools(role)
		if !ok || len(tools) == 0 {
			t.Fatalf("%s: %v, %v", role, tools, ok)
		}
		if slices.Contains(tools, "Edit") || slices.Contains(tools, "Write") {
			t.Errorf("%s is meant to be read-only but its stub lists %v", role, tools)
		}
	}
	for _, name := range []string{"manager", "scrummaster", "nope", "", "../agents/qa", "qa.md"} {
		if tools, ok := AgentTools(name); ok {
			t.Errorf("AgentTools(%q) = %v, want no stub", name, tools)
		}
	}
}

// The Python hooks this replaces. A project scaffolded by the previous build has
// `python3 "$CLAUDE_PROJECT_DIR/.claude/hooks/x.py"` commands and the scripts on
// disk: init must swap the commands (not run both) and say the scripts are stale.
func TestWriteMigratesThePythonHooksToAclineHook(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	os.MkdirAll(filepath.Join(claudeDir, "hooks"), 0o755)
	for _, name := range []string{"pre_tool_use.py", "session_start.py"} {
		os.WriteFile(filepath.Join(claudeDir, "hooks", name), []byte("# old"), 0o755)
	}
	prev := `{"hooks":{` +
		`"SessionStart":[{"hooks":[{"type":"command","command":"python3 \"$CLAUDE_PROJECT_DIR/.claude/hooks/session_start.py\""}]}],` +
		`"PreCompact":[{"hooks":[{"type":"command","command":"python3 \"$CLAUDE_PROJECT_DIR/.claude/hooks/pre_compact.py\""}]}],` +
		`"SessionEnd":[{"hooks":[{"type":"command","command":"python3 \"$CLAUDE_PROJECT_DIR/.claude/hooks/session_end.py\""}]}],` +
		`"PreToolUse":[{"matcher":"Read|Edit|Write|Grep|Glob|Bash|NotebookEdit","hooks":[{"type":"command","command":"python3 \"$CLAUDE_PROJECT_DIR/.claude/hooks/pre_tool_use.py\""}]}]}}`
	settings := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settings, []byte(prev), 0o644)

	result, err := Write(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range hookCommands(t, settings) {
		if strings.Contains(c, "python") {
			t.Errorf("Python command survived: %q", c)
		}
	}
	if got := hookCommands(t, settings); len(got) != 6 {
		t.Errorf("want exactly the six acline hook commands, got %v", got)
	}
	warned := false
	for _, w := range result.Warnings {
		if strings.Contains(w, ".claude/hooks/pre_tool_use.py") && strings.Contains(w, "no longer used") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected a warning about the stale scripts, got %v", result.Warnings)
	}
	// the scripts are the user's to delete: init must not remove files
	if _, err := os.Stat(filepath.Join(claudeDir, "hooks", "pre_tool_use.py")); err != nil {
		t.Error("init deleted a file it did not create this run")
	}
	// and a clean scaffold warns about nothing
	if r, _ := Write(t.TempDir()); len(r.Warnings) != 0 {
		t.Errorf("a fresh scaffold should not warn: %v", r.Warnings)
	}
}

// The guard hook is one layer; Claude Code's own permission rules are a second
// that does not depend on the `acline` binary being reachable.
func settingsPermissions(t *testing.T, path string) (deny, allow []string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Permissions struct {
			Deny  []string `json:"deny"`
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.Permissions.Deny, cfg.Permissions.Allow
}

func TestShippedSettingsDenyTheObviousDangers(t *testing.T) {
	root := t.TempDir()
	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}
	deny, _ := settingsPermissions(t, filepath.Join(root, ".claude", "settings.json"))
	for _, want := range []string{"Read(~/.ssh/**)", "Read(~/.aws/**)", "Read(./.env)", "Read(**/.env)", "Read(**/.env.*)", "Bash(acline auth *)", "Bash(acline snapshot import*)", "Bash(acline project add*)", "Bash(sqlite3 *)", "Edit(.claude/settings.json)", "Bash(git push --force*)", "Bash(git push -f*)", "Bash(git reset --hard*)"} {
		if !slices.Contains(deny, want) {
			t.Errorf("permissions.deny is missing %q: %v", want, deny)
		}
	}
	data, _ := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if !strings.Contains(string(data), `"$schema"`) {
		t.Error("settings.json should name its schema so editors can validate it")
	}
}

func TestWriteMergesPermissionsWithoutDroppingTheUsersRules(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	settings := filepath.Join(claudeDir, "settings.json")
	os.WriteFile(settings, []byte(`{"permissions":{"allow":["Bash(make *)"],"deny":["Bash(git *)","Read(~/.aws/**)"]}}`), 0o644)

	if _, err := Write(root); err != nil {
		t.Fatal(err)
	}
	deny, allow := settingsPermissions(t, settings)
	if !slices.Contains(deny, "Bash(git *)") || !slices.Contains(allow, "Bash(make *)") {
		t.Fatalf("the user's own rules were dropped: deny=%v allow=%v", deny, allow)
	}
	if !slices.Contains(deny, "Read(./.env)") {
		t.Errorf("acline's rules were not added: %v", deny)
	}
	count := map[string]int{}
	for _, d := range deny {
		count[d]++
	}
	if count["Read(~/.aws/**)"] != 1 {
		t.Errorf("a rule the user already had was duplicated: %v", deny)
	}
	data, _ := os.ReadFile(settings)
	if strings.Contains(string(data), `"$schema"`) {
		t.Error("an existing file should not gain a $schema key")
	}
	before := string(data)
	if result, err := Write(root); err != nil || len(result.Merged) != 0 {
		t.Fatalf("a second run should change nothing: %+v, %v", result, err)
	}
	if after, _ := os.ReadFile(settings); string(after) != before {
		t.Error("a second run rewrote the settings")
	}
}
