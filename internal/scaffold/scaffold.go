package scaffold

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// assets contains the Claude Code integration shipped with the binary. Keeping
// it embedded makes `acline init` work after `go install`, without needing the
// ACLine source checkout beside the executable.
//
// assets/skills is edited directly -- this is its only copy; there is no
// separate repo-root skills/ to keep in sync. The hooks are not files any more:
// settings.json runs `acline hook <event>` (see internal/cmd/hook.go), so there is
// no Python runtime dependency and nothing under .claude/hooks to upgrade.
//
// assets/vault is different: it's a one-time-seeded starter template, not
// kept in lockstep with anything. This repo's own root-level vault/ is a
// live working vault (Claude Code session hooks write into it while
// dogfooding ACLine against itself), so it's deliberately NOT the same
// directory as assets/vault -- collapsing them would let live dogfood notes
// leak into the template shipped to every downstream project on the next
// build. If you want a vault/ template change to reach new users, edit
// assets/vault directly.
//
//go:embed all:assets
var assets embed.FS

type Result struct {
	Created  []string
	Skipped  []string
	Merged   []string
	Upgraded []string
	Warnings []string
}

// Write creates .claude/skills and .claude/vault, and registers the hooks in
// settings.json. Existing skill/vault files are never
// overwritten; settings are merged structurally so unrelated configuration is
// preserved. Equivalent to WriteOpts(root, Options{}).
func Write(root string) (Result, error) {
	return WriteOpts(root, Options{})
}

// Options controls Write's behavior beyond the default "never touch an
// existing file" scaffold.
type Options struct {
	// Upgrade overwrites an existing skills/ file with the
	// embedded version when its content differs -- for pulling in fixes to
	// acline's own bundled files into an already-scaffolded project. vault/ is never touched even with Upgrade
	// set: it's a one-time-seeded starter template, not kept in lockstep
	// (see the package doc comment on assets), so a project's vault notes
	// are never at risk of being overwritten. settings.json keeps using its
	// existing structural merge regardless of Upgrade.
	Upgrade bool
}

// WriteOpts is Write with Options controlling how an already-scaffolded
// project is handled.
func WriteOpts(root string, opts Options) (Result, error) {
	var result Result
	err := fs.WalkDir(assets, "assets", func(assetPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// Bytecode from running the hook tests locally isn't ignored by
			// go:embed's all: prefix, so keep it out of downstream projects.
			if entry.Name() == "__pycache__" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(assetPath, ".pyc") {
			return nil
		}
		rel, err := filepath.Rel("assets", assetPath)
		if err != nil {
			return err
		}
		target := filepath.Join(root, ".claude", rel)
		if rel == "settings.json" {
			return mergeSettings(assetPath, target, &result)
		}
		upgradable := opts.Upgrade && strings.HasPrefix(rel, "skills"+string(filepath.Separator))
		if existing, err := os.ReadFile(target); err == nil {
			data, rerr := assets.ReadFile(assetPath)
			if rerr != nil {
				return rerr
			}
			if !upgradable || string(existing) == string(data) {
				result.Skipped = append(result.Skipped, target)
				return nil
			}
			if err := os.WriteFile(target, data, filePermFor(target)); err != nil {
				return fmt.Errorf("upgrading %s: %w", target, err)
			}
			result.Upgraded = append(result.Upgraded, target)
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := assets.ReadFile(assetPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, filePermFor(target)); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		result.Created = append(result.Created, target)
		return nil
	})
	if err == nil {
		if stale := staleHookScripts(root); len(stale) > 0 {
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s are no longer used: hooks are `acline hook <event>` now and the settings above point at them; delete .claude/hooks when you have checked it holds nothing of your own",
				strings.Join(stale, ", ")))
		}
	}
	return result, err
}

// staleHookScripts lists the Python hook scripts an older acline left under
// .claude/hooks.
func staleHookScripts(root string) []string {
	var found []string
	for _, name := range legacyHookScripts {
		if _, err := os.Stat(filepath.Join(root, ".claude", "hooks", name)); err == nil {
			found = append(found, filepath.ToSlash(filepath.Join(".claude", "hooks", name)))
		}
	}
	return found
}

func filePermFor(target string) os.FileMode {
	if filepath.Ext(target) == ".py" {
		return 0o755
	}
	return 0o644
}

func mergeSettings(assetPath, target string, result *Result) error {
	shipped, err := assets.ReadFile(assetPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, shipped, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", target, err)
		}
		result.Created = append(result.Created, target)
		return nil
	} else if err != nil {
		return err
	}

	existing, err := os.ReadFile(target)
	if err != nil {
		return err
	}
	var current, wanted map[string]any
	if err := json.Unmarshal(existing, &current); err != nil {
		// A hand-edited or JSONC-commented settings.json shouldn't abort the
		// rest of the scaffold — just skip the merge for this file and let
		// every other asset still get written.
		result.Warnings = append(result.Warnings, fmt.Sprintf("could not parse existing %s (left unchanged): %v", target, err))
		result.Skipped = append(result.Skipped, target)
		return nil
	}
	if err := json.Unmarshal(shipped, &wanted); err != nil {
		return fmt.Errorf("parsing embedded settings: %w", err)
	}
	changed := mergeHooks(current, wanted)
	if mergeEnv(current, wanted) {
		changed = true
	}
	if mergePermissions(current, wanted) {
		changed = true
	}
	if !changed {
		result.Skipped = append(result.Skipped, target)
		return nil
	}
	data, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return fmt.Errorf("updating %s: %w", target, err)
	}
	result.Merged = append(result.Merged, target)
	return nil
}

func mergeHooks(current, wanted map[string]any) bool {
	wantedHooks, _ := wanted["hooks"].(map[string]any)
	currentHooks, ok := current["hooks"].(map[string]any)
	if !ok {
		currentHooks = make(map[string]any)
		current["hooks"] = currentHooks
	}
	changed := false
	for event, rawEntries := range wantedHooks {
		wantedEntries, _ := rawEntries.([]any)
		currentEntries, _ := currentHooks[event].([]any)
		currentEntries, removed := dropLegacyHooks(currentEntries)
		if removed {
			changed = true
		}
		currentEntries, superseded := dropSupersededAclineHooks(currentEntries, wantedEntries)
		if superseded {
			changed = true
		}
		for _, wantedEntry := range wantedEntries {
			if !containsJSON(currentEntries, wantedEntry) {
				currentEntries = append(currentEntries, wantedEntry)
				changed = true
			}
		}
		currentHooks[event] = currentEntries
	}
	return changed
}

// dropSupersededAclineHooks removes entries an older acline scaffolded that this
// build has replaced: an entry that is not one of the wanted entries but whose
// every hook command is a command the wanted entries also use (same script, an
// older matcher). Without it a changed matcher would be appended beside the old
// entry and the guard would run twice per tool call. Entries with any command
// acline does not ship are the user's and are left alone.
func dropSupersededAclineHooks(entries, wanted []any) ([]any, bool) {
	wantedCommands := map[string]bool{}
	for _, w := range wanted {
		for _, c := range entryCommands(w) {
			wantedCommands[c] = true
		}
	}
	dropped := false
	kept := entries[:0:0]
	for _, entry := range entries {
		cmds := entryCommands(entry)
		if len(cmds) == 0 || containsJSON(wanted, entry) {
			kept = append(kept, entry)
			continue
		}
		ours := true
		for _, c := range cmds {
			if !wantedCommands[c] {
				ours = false
			}
		}
		if ours {
			dropped = true
			continue
		}
		kept = append(kept, entry)
	}
	return kept, dropped
}

// entryCommands lists the command strings of one settings hook entry.
func entryCommands(entry any) []string {
	e, _ := entry.(map[string]any)
	hooks, _ := e["hooks"].([]any)
	var out []string
	for _, h := range hooks {
		hook, _ := h.(map[string]any)
		if c, _ := hook["command"].(string); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func containsJSON(items []any, candidate any) bool {
	want, _ := json.Marshal(candidate)
	for _, item := range items {
		got, _ := json.Marshal(item)
		if string(got) == string(want) {
			return true
		}
	}
	return false
}

// legacyHookCommand matches the Python hook commands older acline builds
// scaffolded: the cwd-relative `python3 .claude/hooks/<name>.py` (which broke as
// soon as the session's shell left the project root) and the later
// `python3 "$CLAUDE_PROJECT_DIR/.claude/hooks/<name>.py"`. Both are replaced by
// `acline hook <event>` rather than kept alongside it, so each hook does not end
// up running twice.
var legacyHookCommand = regexp.MustCompile(`^python3 ("\$CLAUDE_PROJECT_DIR/)?\.claude/hooks/[a-z_]+\.py"?$`)

// legacyHookScripts are the Python files those commands ran.
var legacyHookScripts = []string{"pre_tool_use.py", "session_start.py", "pre_compact.py", "session_end.py", "_memory_log.py"}

// dropLegacyHooks removes legacy acline hook commands from an event's
// entries, and any entry left with no hooks as a result. Entries the user
// added are untouched.
func dropLegacyHooks(entries []any) ([]any, bool) {
	removed := false
	kept := entries[:0:0]
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		hooks, hasHooks := entry["hooks"].([]any)
		if !ok || !hasHooks {
			kept = append(kept, raw)
			continue
		}
		var keptHooks []any
		for _, h := range hooks {
			hook, _ := h.(map[string]any)
			command, _ := hook["command"].(string)
			if legacyHookCommand.MatchString(command) {
				removed = true
				continue
			}
			keptHooks = append(keptHooks, h)
		}
		if len(keptHooks) == 0 && len(hooks) > 0 {
			continue
		}
		entry["hooks"] = append([]any{}, keptHooks...)
		kept = append(kept, entry)
	}
	return kept, removed
}

// mergePermissions adds the shipped permission rules the project does not already
// have, per list (deny, allow, ask). The user's own rules are never removed or
// reordered, and a rule they already have is not repeated.
func mergePermissions(current, wanted map[string]any) bool {
	wantedPerms, _ := wanted["permissions"].(map[string]any)
	if len(wantedPerms) == 0 {
		return false
	}
	currentPerms, ok := current["permissions"].(map[string]any)
	if !ok {
		currentPerms = make(map[string]any)
		current["permissions"] = currentPerms
	}
	changed := false
	for list, raw := range wantedPerms {
		wantedRules, _ := raw.([]any)
		have, _ := currentPerms[list].([]any)
		present := map[string]bool{}
		for _, r := range have {
			if rule, ok := r.(string); ok {
				present[rule] = true
			}
		}
		for _, r := range wantedRules {
			if rule, ok := r.(string); ok && !present[rule] {
				have = append(have, rule)
				present[rule] = true
				changed = true
			}
		}
		if len(have) > 0 {
			currentPerms[list] = have
		}
	}
	return changed
}

// mergeEnv adds the shipped env vars that the project doesn't already set.
// An existing value always wins, so a project can override the actor.
func mergeEnv(current, wanted map[string]any) bool {
	wantedEnv, _ := wanted["env"].(map[string]any)
	if len(wantedEnv) == 0 {
		return false
	}
	currentEnv, ok := current["env"].(map[string]any)
	if !ok {
		currentEnv = make(map[string]any)
		current["env"] = currentEnv
	}
	changed := false
	for key, value := range wantedEnv {
		if _, exists := currentEnv[key]; !exists {
			currentEnv[key] = value
			changed = true
		}
	}
	return changed
}

// RoleContract returns the bundled behavior contract for a built-in role (the
// body of assets/vault/roles/<name>.md, front matter removed), or false for a
// role acline does not ship one for (e.g. a project's own custom role).
func RoleContract(name string) (string, bool) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "", false
	}
	b, err := assets.ReadFile("assets/vault/roles/" + name + ".md")
	if err != nil {
		return "", false
	}
	s := string(b)
	if rest, ok := strings.CutPrefix(s, "---\n"); ok {
		if _, body, found := strings.Cut(rest, "\n---\n"); found {
			s = body
		}
	}
	return strings.TrimSpace(s), true
}

// AgentTools returns the tools a built-in role's agent stub grants (the `tools:`
// line of assets/agents/<name>.md), or false for a role acline ships no stub for
// (a project's own role, manager, scrummaster). The guard uses it to hold a
// session running as a read-only role to its contract.
func AgentTools(name string) ([]string, bool) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return nil, false
	}
	b, err := assets.ReadFile("assets/agents/" + name + ".md")
	if err != nil {
		return nil, false
	}
	rest, ok := strings.CutPrefix(string(b), "---\n")
	if !ok {
		return nil, false
	}
	front, _, found := strings.Cut(rest, "\n---\n")
	if !found {
		return nil, false
	}
	for _, line := range strings.Split(front, "\n") {
		if list, ok := strings.CutPrefix(line, "tools:"); ok {
			var tools []string
			for _, tool := range strings.Split(list, ",") {
				if tool = strings.TrimSpace(tool); tool != "" {
					tools = append(tools, tool)
				}
			}
			return tools, true
		}
	}
	return nil, false
}

// RoleIsReadOnly reports whether a built-in role's contract grants no file
// writes (its agent stub lists neither Edit nor Write). The guard denies such a
// session's writes, and the store refuses an agent that tries to end one, so
// the contract and both enforcements read the same stub.
func RoleIsReadOnly(name string) bool {
	tools, ok := AgentTools(name)
	if !ok {
		return false
	}
	for _, t := range tools {
		if t == "Edit" || t == "Write" {
			return false
		}
	}
	return true
}
