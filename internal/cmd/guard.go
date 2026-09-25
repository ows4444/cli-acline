package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"acline/internal/store"
)

// guard.go ports second-brain-pro's Python security.py + pre_tool_use.py
// into Go, as the one place write-scope/secret policy lives (the PreToolUse hook,
// `acline hook pre-tool-use` in hook.go, calls guardCheckTool). The
// pattern lists and the escape-normalization/subshell-extraction logic are
// carried over verbatim from the Python version, including the known
// `$(echo rm\ -rf\ /)` bypass fix.

var secretPathPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\.env(\..*)?$`),
	regexp.MustCompile(`(?i)\.pem$`),
	regexp.MustCompile(`(?i)\.key$`),
	regexp.MustCompile(`(?i)\.pfx$`),
	regexp.MustCompile(`(?i)\.p12$`),
	regexp.MustCompile(`(?i)credentials\.json$`),
	regexp.MustCompile(`(?i)google_token\.json$`),
	regexp.MustCompile(`(?i)token\.json$`),
	regexp.MustCompile(`(?i)secrets?\.ya?ml$`),
	regexp.MustCompile(`(?i)\.npmrc$`),
	regexp.MustCompile(`(?i)\.netrc$`),
	regexp.MustCompile(`(?i)id_rsa(\.pub)?$`),
	regexp.MustCompile(`(?i)id_ed25519(\.pub)?$`),
	regexp.MustCompile(`(?i)\.ssh/`),
	regexp.MustCompile(`(?i)\.aws/credentials$`),
	regexp.MustCompile(`(?i)\.pypirc$`),
	// Cluster, container-registry, git, database, cloud and infrastructure
	// credentials. Terraform state routinely holds plaintext secrets.
	regexp.MustCompile(`(?i)\.kube/config$`),
	regexp.MustCompile(`(?i)(^|/)kubeconfig$`),
	regexp.MustCompile(`(?i)\.docker/config\.json$`),
	regexp.MustCompile(`(?i)\.git-credentials$`),
	regexp.MustCompile(`(?i)\.pgpass$`),
	regexp.MustCompile(`(?i)\.tfstate(\.backup)?$`),
	regexp.MustCompile(`(?i)id_ecdsa(\.pub)?$`),
	regexp.MustCompile(`(?i)\.p8$`),
	regexp.MustCompile(`(?i)\.config/gcloud/`),
	regexp.MustCompile(`(?i)\.azure/`),
}

var secretBashPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bcat\s+.*\.env\b`),
	regexp.MustCompile(`(?i)\bprintenv\b`),
	regexp.MustCompile(`(?i)\benv\b\s*(?:$|[|>])`),
	regexp.MustCompile(`(?i)\becho\s+\$[A-Z_]+`),
	regexp.MustCompile(`(?i)\bexport\s*$`),
}

// interpreterSecretPatterns only mean something inside code an interpreter
// runs, so they apply to raw-fallback targets only (see bashScanTargets). A
// command with no interpreter in it can only contain them as data, e.g.
// `grep -n "os.environ" scripts/*.py` or a note that mentions them.
var interpreterSecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)os\.environ`),
	regexp.MustCompile(`(?i)\bgetenv\(`),
}

// dangerousBashPatterns blocks commands that cause irreversible data loss or
// system compromise with no legitimate routine use (unlike, say, `sudo` or
// `npm install`, which are ordinary dev actions — see routineBashPatterns).
var dangerousBashPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*r[a-zA-Z]*f\b`),
	regexp.MustCompile(`(?i)\brm\s+-[a-zA-Z]*f[a-zA-Z]*r\b`),
	regexp.MustCompile(`(?i)\brm\s+-rf\s+/`),
	regexp.MustCompile(`(?i)\bdd\s+if=`),
	regexp.MustCompile(`(?i)\bmkfs\.`),
	regexp.MustCompile(`:\(\)\{.*\};:`),
	regexp.MustCompile(`(?i)\bshred\b`),
	regexp.MustCompile(`>\s*/dev/sd[a-z]`),
	regexp.MustCompile(`(?i)\bcurl\b.*\|\s*(sh|bash)\b`),
	regexp.MustCompile(`(?i)\bwget\b.*\|\s*(sh|bash)\b`),
	// History rewrites and deletions that cannot be undone from the working tree
	// or that overwrite what others have pulled. Matched against the shell view,
	// so a commit message that merely mentions them is not blocked.
	regexp.MustCompile(`(?i)\bgit\b[^;&|\n]*\bpush\b[^;&|\n]*(\s--force(-with-lease)?\b|\s-[a-zA-Z]*f[a-zA-Z]*\b|\s\+\S)`),
	regexp.MustCompile(`(?i)\bgit\b[^;&|\n]*\breset\b[^;&|\n]*\s--hard\b`),
	regexp.MustCompile(`(?i)\bgit\b[^;&|\n]*\bclean\b[^;&|\n]*(\s--force\b|\s-[a-zA-Z]*f)`),
	regexp.MustCompile(`(?i)\bfind\b[^;&|\n]*\s-delete\b`),
	regexp.MustCompile(`(?i)\bfind\b[^;&|\n]*\s-exec(dir)?\s+rm\b`),
}

// routineBashPatterns match commands that carry some risk (privilege
// escalation, broad permission changes, pulling in dependencies) but are
// routine, frequent parts of normal development. Blocking these outright
// trained users to reach for --force/override, which erodes the audit
// trail's signal; instead they're allowed through and logged (guard_warned)
// so the audit trail still records that they happened.
var routineBashPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bpip3?\s+install\b`),
	regexp.MustCompile(`(?i)\bnpm\s+(install|i)\b`),
	regexp.MustCompile(`(?i)\byarn\s+add\b`),
	regexp.MustCompile(`(?i)\bpoetry\s+add\b`),
	regexp.MustCompile(`(?i)\bbrew\s+install\b`),
	regexp.MustCompile(`(?i)\bapt(-get)?\s+install\b`),
	regexp.MustCompile(`(?i)\bgem\s+install\b`),
	regexp.MustCompile(`(?i)\bcargo\s+install\b`),
	regexp.MustCompile(`(?i)\bsudo\b`),
	regexp.MustCompile(`(?i)\bchmod\s+777\b`),
	regexp.MustCompile(`(?i)\bchmod\s+-[a-zA-Z]*R`),
	regexp.MustCompile(`(?i)\bchown\s+-R\b`),
}

// binaryPrefixRe strips an absolute-path binary prefix (e.g. `/usr/bin/rm`
// -> `rm`) so pattern matching below still catches a fully-qualified
// invocation. The match must start at the beginning of the command or
// right after a shell separator/whitespace — not just anywhere \b permits.
// An earlier version anchored on \b alone, which let the alternation match
// `/bin/` as a substring starting *inside* `/usr/bin/` (there's a word
// boundary between "usr" and the following "/"), mangling
// "/usr/bin/rm -rf /" into "/usrrm -rf /" — which no longer matches the
// `\brm\b`-anchored dangerous-command patterns below, silently defeating
// the exact detection this was meant to strengthen.
var binaryPrefixRe = regexp.MustCompile(`(^|[\s;&|(])(?:/usr/bin/|/usr/local/bin/|/bin/|/opt/homebrew/bin/)`)
var subshellRe = regexp.MustCompile("\\$\\(([^()]*)\\)|`([^`]*)`")
var segmentSplitRe = regexp.MustCompile(`[;&|\n]+`)

func stripBinaryPrefixes(cmd string) string {
	return binaryPrefixRe.ReplaceAllString(cmd, "$1")
}

// normalizeCmd undoes cheap shell-escaping tricks before pattern matching,
// e.g. `rm\ -rf\ /` (backslash-escaped spaces) reads as `rm -rf /` to a shell
// but would silently dodge a naive `\s+` regex without this.
func normalizeCmd(cmd string) string {
	return stripBinaryPrefixes(escapedBlankRe.ReplaceAllString(cmd, " "))
}

// escapedBlankRe is a backslash-escaped space, newline or tab.
var escapedBlankRe = regexp.MustCompile(`\\[ \n\t]`)

func extractSubshells(cmd string, depth int) []string {
	if depth > 5 {
		return nil
	}
	var found []string
	for _, m := range subshellRe.FindAllStringSubmatch(cmd, -1) {
		inner := m[1]
		if inner == "" {
			inner = m[2]
		}
		if inner != "" {
			found = append(found, inner)
			found = append(found, extractSubshells(inner, depth+1)...)
		}
	}
	return found
}

// checkBashCommand runs the policy patterns over bashScanTargets (see
// guard_shell.go), so quoted text and heredoc bodies that are only data
// don't trip them, while anything bash would execute still does.
func checkBashCommand(cmd string) (bool, string) {
	for _, target := range bashScanTargets(cmd, 0) {
		c := target.text
		if hasRecursiveForcedRemove(c) {
			return true, fmt.Sprintf("blocked: command recursively force-removes files (in: %s)", truncate(c, 100))
		}
		for _, p := range dangerousBashPatterns {
			if p.MatchString(c) {
				return true, fmt.Sprintf("blocked: command matches dangerous pattern `%s` (in: %s)", p.String(), truncate(c, 100))
			}
		}
		secretPatterns := secretBashPatterns
		if target.raw {
			secretPatterns = append(append([]*regexp.Regexp{}, secretBashPatterns...), interpreterSecretPatterns...)
		}
		for _, p := range secretPatterns {
			if p.MatchString(c) {
				return true, fmt.Sprintf("blocked: command would expose secrets/env vars (in: %s)", truncate(c, 100))
			}
		}
		dbPath := ""
		if st != nil {
			dbPath = st.Path
		}
		if reason := bashSensitiveAccess(c, dbPath); reason != "" {
			return true, fmt.Sprintf("%s (in: %s)", reason, truncate(c, 100))
		}
		if bashWritesProtectedVaultFile(c) {
			return true, fmt.Sprintf("blocked: SOUL.md, role persona files under vault/roles/ and .claude/settings*.json (which run the guard) are write-protected, including via Bash — record suggested changes with: acline note add \"suggested <file> change: ...\" (in: %s)", truncate(c, 100))
		}
	}
	return false, ""
}

// SOUL.md write protection for Bash. Edit/Write are covered by
// checkProtectedVaultFile's path comparison, but a shell command has no single
// target path, so this is a conservative per-segment heuristic: a segment
// that names SOUL.md or a role persona file under vault/roles/ (case-
// insensitive, since macOS filesystems are) and uses a write-capable
// operation is denied. Plain reads (cat, grep, less, cp SOUL.md elsewhere)
// stay allowed. Interpreter one-liners that name it are denied outright
// since read vs. write can't be told apart. Role persona files (acline
// spec #2) get the same protection as SOUL.md: they define behavioral
// boundaries per role, the same category of thing SOUL.md protects, so
// they use one check rather than a second, inconsistent one.
var (
	protectedVaultNameRe = regexp.MustCompile(`(?i)SOUL\.md|roles/[^/\s'"` + "`" + `]+\.md|\.claude/settings(?:\.local)?\.json`)
	protectedVaultRdrRe  = regexp.MustCompile(`(?i)>{1,2}\|?\s*["']?[^\s;&|]*(SOUL\.md|roles/[^/\s'"` + "`" + `]+\.md|\.claude/settings(?:\.local)?\.json)`)
	protectedVaultInPl   = regexp.MustCompile(`(?i)\b(sed|perl|ruby)\b.*\s-[a-zA-Z]*i`)
	protectedVaultAnyUse = map[string]bool{"tee": true, "mv": true, "rm": true, "ln": true, "truncate": true, "chmod": true, "chown": true, "touch": true, "install": true, "dd": true}
	protectedVaultInterp = map[string]bool{"python": true, "python3": true, "node": true, "perl": true, "ruby": true, "sh": true, "bash": true, "zsh": true}
	protectedVaultGitRe  = regexp.MustCompile(`(?i)\bgit\s+(checkout|restore|apply|mv|rm)\b`)
	protectedVaultCopyTo = map[string]bool{"cp": true, "rsync": true}
)

func bashWritesProtectedVaultFile(cmd string) bool {
	if !protectedVaultNameRe.MatchString(cmd) {
		return false
	}
	if protectedVaultRdrRe.MatchString(cmd) {
		return true
	}
	for _, segment := range segmentSplitRe.Split(cmd, -1) {
		if !protectedVaultNameRe.MatchString(segment) {
			continue
		}
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		name := filepath.Base(fields[0])
		switch {
		case protectedVaultAnyUse[name], protectedVaultInterp[name]:
			return true
		case protectedVaultCopyTo[name]:
			if protectedVaultNameRe.MatchString(strings.Trim(fields[len(fields)-1], `"'`)) {
				return true
			}
		case protectedVaultInPl.MatchString(segment), protectedVaultGitRe.MatchString(segment):
			return true
		}
	}
	return false
}

// checkBashCommandWarnings reports (without blocking) that cmd matches a
// routineBashPatterns entry, so the caller can log it to the audit trail.
func checkBashCommandWarnings(cmd string) string {
	for _, target := range bashScanTargets(cmd, 0) {
		c := target.text
		for _, p := range routineBashPatterns {
			if p.MatchString(c) {
				return fmt.Sprintf("command matches routine but risk-worth-logging pattern `%s` (in: %s)", p.String(), truncate(c, 100))
			}
		}
	}
	return ""
}

// hasRecursiveForcedRemove recognizes both combined and separate short flags,
// plus GNU long options. The regex list alone cannot reliably express "both
// options occur, in either order" because Go's regexp engine has no lookahead.
func hasRecursiveForcedRemove(cmd string) bool {
	for _, segment := range segmentSplitRe.Split(cmd, -1) {
		fields := strings.Fields(segment)
		for i, field := range fields {
			if filepath.Base(field) != "rm" {
				continue
			}
			var recursive, force bool
			for _, arg := range fields[i+1:] {
				switch arg {
				case "--recursive":
					recursive = true
				case "--force":
					force = true
				default:
					if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
						flags := strings.TrimPrefix(arg, "-")
						recursive = recursive || strings.Contains(flags, "r") || strings.Contains(flags, "R")
						force = force || strings.Contains(flags, "f")
					}
				}
			}
			if recursive && force {
				return true
			}
		}
	}
	return false
}

// truncate trims s and cuts it to at most n runes (not bytes, so a multibyte
// character in a denied command is never split into invalid UTF-8).
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// realPath is store.RealPath: the symlink-free absolute form every scope and
// protection check compares against (see there for why EvalSymlinks is not
// enough). The store's session policy uses the same resolution.
func realPath(path string) string { return store.RealPath(path) }

// checkFilePathForSecrets matches the requested path string itself, then
// (if different) its symlink-resolved real path — a symlink with an
// innocuous name pointing at e.g. ~/.ssh/id_rsa would otherwise dodge this
// check on a Read since only the literal requested path was ever examined.
func checkFilePathForSecrets(path string) (bool, string) {
	for _, p := range secretPathPatterns {
		if p.MatchString(path) {
			return true, fmt.Sprintf("blocked: '%s' matches a protected secret-file pattern", path)
		}
	}
	if resolved := realPath(path); resolved != path {
		for _, p := range secretPathPatterns {
			if p.MatchString(resolved) {
				return true, fmt.Sprintf("blocked: '%s' resolves to a protected secret-file pattern (%s)", path, resolved)
			}
		}
	}
	return false, ""
}

// checkProtectedVaultFile denies a write to SOUL.md or any role persona
// file under vault/roles/ (acline spec #2) — both define behavioral
// boundaries, so both get the same write protection and the same escape
// hatch (propose the change as a note instead of editing directly).
func checkProtectedVaultFile(vaultRoot, path string) (bool, string) {
	resolved := realPath(path)
	// The project's Claude Code settings run this guard; an agent that could edit
	// them could remove it. `acline init --upgrade` rewrites them for a person.
	if base := strings.ToLower(filepath.Base(resolved)); (base == "settings.json" || base == "settings.local.json") && filepath.Base(filepath.Dir(resolved)) == ".claude" {
		return true, fmt.Sprintf("blocked: '%s' configures the guard hook and is write-protected — ask the user to change it, or run `acline init --upgrade` to restore it", path)
	}
	if resolved == realPath(filepath.Join(vaultRoot, "SOUL.md")) {
		return true, "blocked: SOUL.md is write-protected — record suggested changes with: acline note add \"suggested SOUL.md change: ...\""
	}
	rolesDir := realPath(filepath.Join(vaultRoot, "roles"))
	if rel, err := filepath.Rel(rolesDir, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && filepath.Ext(resolved) == ".md" {
		return true, fmt.Sprintf("blocked: '%s' is a role persona file and is write-protected — record suggested changes with: acline note add \"suggested %s change: ...\"", path, filepath.Base(resolved))
	}
	return false, ""
}

// guardAllProjectsEnv, set to 1 in the hook's environment (settings.json
// `env`), lets a session write to every tracked project instead of only the one
// it runs in.
const guardAllProjectsEnv = "ACLINE_GUARD_ALL_PROJECTS"

// allowedWriteRoots is the boundary an Edit/Write must stay inside: the vault
// plus the project this session runs in. When the working directory resolves
// to no project (a single-project store that never registered one), or
// ACLINE_GUARD_ALL_PROJECTS=1, it is the vault plus every tracked project's
// path. Each root is resolved through realPath too: a root that is itself a
// symlink (e.g. a vault synced via a symlinked folder) must still compare
// correctly against a resolved candidate path.
func allowedWriteRoots(vaultRoot string) []string {
	roots := []string{realPath(vaultRoot)}
	if st == nil {
		return roots
	}
	if os.Getenv(guardAllProjectsEnv) != "1" {
		if cur, err := st.ResolveCurrentProject(); err == nil && cur.Path.Valid && cur.Path.String != "" {
			return append(roots, realPath(cur.Path.String))
		}
	}
	if projects, err := st.ListProjects(); err == nil {
		for _, p := range projects {
			if p.Path.Valid && p.Path.String != "" {
				roots = append(roots, realPath(p.Path.String))
			}
		}
	}
	return roots
}

func checkWriteScope(vaultRoot, path string) (bool, string) {
	resolved := realPath(path)
	for _, root := range allowedWriteRoots(vaultRoot) {
		if resolved == root {
			return false, ""
		}
		// Only a leading ".." component means "outside root". A plain
		// leading '.' is a dot-directory inside it (.claude/, .github/),
		// which an earlier `rel[0] != '.'` check wrongly rejected.
		if rel, err := filepath.Rel(root, resolved); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false, ""
		}
	}
	return true, fmt.Sprintf("blocked: '%s' is outside the vault and this session's project — ask the user before touching files elsewhere (a person can set %s=1 to allow every tracked project)", path, guardAllProjectsEnv)
}

// isSessionScratchpad reports whether path is inside the scratchpad Claude
// Code gives the current session for temporary files:
// <tmp>/claude-<uid>/<project>/<session_id>/scratchpad/. Claude Code's
// system prompt tells the agent to write there, so checkWriteScope alone
// contradicted it. Only the session named
// in the hook payload is allowed; other sessions' scratchpads and the rest
// of the temp dir stay outside scope. The path is symlink-resolved first,
// so a link inside the scratchpad can't point the write elsewhere.
func isSessionScratchpad(path, sessionID string) bool {
	if sessionID == "" || sessionID != filepath.Base(sessionID) || strings.HasPrefix(sessionID, ".") {
		return false
	}
	resolved := realPath(path)
	for _, base := range []string{realPath("/tmp"), realPath(os.TempDir())} {
		rel, err := filepath.Rel(base, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 4 && strings.HasPrefix(parts[0], "claude-") && parts[2] == sessionID && parts[3] == "scratchpad" {
			return true
		}
	}
	return false
}

// dbPathIsProtected additionally blocks direct Edit/Write on the SDLC
// database/snapshot files — mutation must go through the `acline` CLI.
// dbPath is the actual resolved store path in use (st.Path), not just the
// default filename — a custom `--db`/`$ACLINE_DB` location is protected too,
// including its WAL/SHM/journal sidecar files.
func dbPathIsProtected(dbPath, path string) (bool, string) {
	base := filepath.Base(path)
	if base == "acline.db" || base == "acline.json" || filepath.Ext(path) == ".db" && filepath.Base(filepath.Dir(path)) == ".acline" {
		return true, fmt.Sprintf("blocked: '%s' is SDLC-managed state — mutate it via the `acline` CLI, not a direct edit", path)
	}
	if dbPath == "" {
		return false, ""
	}
	resolved := realPath(path)
	resolvedDB := realPath(dbPath)
	for _, candidate := range []string{resolvedDB, resolvedDB + "-wal", resolvedDB + "-shm", resolvedDB + "-journal"} {
		if resolved == candidate {
			return true, fmt.Sprintf("blocked: '%s' is SDLC-managed state — mutate it via the `acline` CLI, not a direct edit", path)
		}
	}
	return false, ""
}

type hookPayload struct {
	SessionID string         `json:"session_id"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
}

type hookDenyOutput struct {
	HookSpecificOutput struct {
		HookEventName            string `json:"hookEventName"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
}

func payloadPath(payload hookPayload) string {
	for _, key := range []string{"file_path", "path", "pattern", "notebook_path"} {
		if path, _ := payload.ToolInput[key].(string); path != "" {
			return path
		}
	}
	return ""
}

// checkActivePolicy connects the policy recorded by `session start` to the
// actual pre-tool-use enforcement path. With no active session there is no
// session policy to apply, so the permanent guard rules remain the authority.
func checkActivePolicy(tool, path string) (bool, string, error) {
	allowed, reason, err := st.CheckPolicy(tool, path)
	if errors.Is(err, store.ErrNoActiveSession) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return !allowed, reason, nil
}

func denyOutput(reason string) hookDenyOutput {
	var out hookDenyOutput
	out.HookSpecificOutput.HookEventName = "PreToolUse"
	out.HookSpecificOutput.PermissionDecision = "deny"
	out.HookSpecificOutput.PermissionDecisionReason = reason
	return out
}

// fileTools/writeTools only matter for a tool Claude Code actually invokes
// this hook for — that's decided separately, by the PreToolUse `matcher`
// string in internal/scaffold/assets/settings.json (and any real project's
// copy of it). Adding a tool here without adding it to that matcher too is a
// no-op fix: `guard check-tool` would correctly deny it, but the hook that
// calls `guard check-tool` never gets invoked for that tool in the first
// place. This happened once already and is checked for at build time
// by TestExampleHookMatcherCoversGuardTools in guard_test.go; keep that
// test in sync with both this map and the matcher string when either
// changes.
var fileTools = map[string]bool{"Read": true, "Edit": true, "Write": true, "Grep": true, "Glob": true, "NotebookEdit": true}
var writeTools = map[string]bool{"Edit": true, "Write": true, "NotebookEdit": true}

// policyOnlyTools are tools guard.go has no content rules for, but which the
// PreToolUse matcher should still cover so a session policy (`session start
// --deny-tools WebFetch`) can deny them: the web tools, subagent launches, and
// every MCP tool (`mcp__.*` is a matcher pattern, not a tool name).
var policyOnlyTools = []string{"WebFetch", "WebSearch", "Task", "mcp__.*"}

// missingPolicyTools is missingFromMatcher for policyOnlyTools.
func missingPolicyTools(matcher string) []string {
	matched := map[string]bool{}
	for _, name := range strings.Split(matcher, "|") {
		matched[strings.TrimSpace(name)] = true
	}
	var missing []string
	for _, tool := range policyOnlyTools {
		if !matched[tool] {
			missing = append(missing, tool)
		}
	}
	return missing
}

// requiredGuardTools returns every tool name guard.go's check-tool RunE
// actually inspects — "Bash" (handled directly) plus every key in
// fileTools. A PreToolUse matcher that doesn't cover all of these will
// silently never invoke `guard check-tool` for the missing tool.
func requiredGuardTools() map[string]bool {
	required := map[string]bool{"Bash": true}
	for tool := range fileTools {
		required[tool] = true
	}
	return required
}

// missingFromMatcher parses a settings.json PreToolUse `matcher` string
// (pipe-separated tool names) and returns which requiredGuardTools() are
// not covered by it, sorted for stable output.
func missingFromMatcher(matcher string) []string {
	matched := map[string]bool{}
	for _, name := range strings.Split(matcher, "|") {
		matched[strings.TrimSpace(name)] = true
	}
	var missing []string
	for tool := range requiredGuardTools() {
		if !matched[tool] {
			missing = append(missing, tool)
		}
	}
	sort.Strings(missing)
	return missing
}

var guardVault string
var guardDoctorRoot string

var guardCmd = &cobra.Command{
	Use:   "guard",
	Short: "Security policy checks for Claude Code hooks",
}

var guardDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check this project's .claude/settings.json PreToolUse matcher against the tools guard.go actually checks",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := filepath.Join(guardDoctorRoot, ".claude", "settings.json")
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("no %s found — nothing to check (run `acline init` to scaffold one)\n", path)
				return nil
			}
			return err
		}
		var cfg struct {
			Hooks struct {
				PreToolUse []struct {
					Matcher string `json:"matcher"`
				} `json:"PreToolUse"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		if len(cfg.Hooks.PreToolUse) == 0 {
			fmt.Printf("%s has no PreToolUse hook — guard check-tool is never invoked for any tool\n", path)
			return nil
		}
		anyMissing := false
		for _, entry := range cfg.Hooks.PreToolUse {
			missing := missingFromMatcher(entry.Matcher)
			if len(missing) > 0 {
				anyMissing = true
				fmt.Printf("%s: PreToolUse matcher %q is missing %s — those tools bypass guard check-tool entirely\n", path, entry.Matcher, strings.Join(missing, ", "))
			}
		}
		for _, entry := range cfg.Hooks.PreToolUse {
			if missing := missingPolicyTools(entry.Matcher); len(missing) > 0 {
				fmt.Printf("%s: advisory: PreToolUse matcher %q does not include %s, so a session policy cannot deny those tools (run `acline init --upgrade`)\n", path, entry.Matcher, strings.Join(missing, ", "))
			}
		}
		if !anyMissing {
			fmt.Printf("%s: PreToolUse matcher covers every tool guard.go checks\n", path)
			return nil
		}
		return errors.New("PreToolUse matcher is missing tool coverage; see above")
	},
}

var guardCheckToolCmd = &cobra.Command{
	Use:   "check-tool",
	Short: "Read a PreToolUse hook payload from stdin, print a deny decision (or nothing) to stdout",
	RunE: func(cmd *cobra.Command, args []string) error {
		return guardCheckTool(os.Stdin, os.Stdout)
	},
}

// guardCheckTool judges one PreToolUse payload read from in and writes Claude
// Code's deny decision to out, or nothing when the call is allowed. It is the one
// implementation behind `acline guard check-tool` and `acline hook pre-tool-use`.
func guardCheckTool(in io.Reader, out io.Writer) error {
	var payload hookPayload
	dec := json.NewDecoder(in)
	if err := dec.Decode(&payload); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty stdin (a manual run, or the wrapper's "{}" for a
			// tty): there is nothing to judge.
			return nil
		}
		// Garbled input from a hook means the caller is broken or being
		// tampered with; answering "allow" would make the guard fail open.
		reason := fmt.Sprintf("blocked: guard could not parse the hook payload (%v)", err)
		logEventGlobal("guard_denied", reason)
		return json.NewEncoder(out).Encode(denyOutput(reason))
	}

	deny := func(reason string) error {
		// Fire-and-forget: a blocked action belongs in the audit trail
		// (the whole point of an append-only, hash-chained events
		// table), but a hook must still answer with its deny decision
		// even if this insert fails for some reason (e.g. a locked
		// db) — logEventGlobal already swallows LogEvent's error for
		// exactly this "secondary side effect" reason.
		logEventGlobal("guard_denied", fmt.Sprintf("tool=%s: %s", payload.ToolName, reason))
		enc := json.NewEncoder(out)
		return enc.Encode(denyOutput(reason))
	}

	path := payloadPath(payload)
	if blocked, reason, err := checkActivePolicy(payload.ToolName, path); err != nil {
		return err
	} else if blocked {
		return deny(reason)
	}
	if blocked, reason := checkRoleScope(payload.ToolName, payload.ToolInput); blocked {
		return deny(reason)
	}

	if payload.ToolName == "Bash" {
		cmdStr, _ := payload.ToolInput["command"].(string)
		if blocked, reason := checkBashCommand(cmdStr); blocked {
			return deny(reason)
		}
		if warning := checkBashCommandWarnings(cmdStr); warning != "" {
			logEventGlobal("guard_warned", warning)
		}
		return nil
	}

	if fileTools[payload.ToolName] {
		// A search reads whatever its glob selects, whatever its path says.
		if glob, _ := payload.ToolInput["glob"].(string); secretGlob(glob) {
			return deny(fmt.Sprintf("blocked: glob %q selects protected secret files", glob))
		}
		if path == "" {
			return nil
		}

		if blocked, reason := checkFilePathForSecrets(path); blocked {
			return deny(reason)
		}

		if writeTools[payload.ToolName] {
			if blocked, reason := checkProtectedVaultFile(guardVault, path); blocked {
				return deny(reason)
			}
			if blocked, reason := dbPathIsProtected(st.Path, path); blocked {
				return deny(reason)
			}
			if blocked, reason := checkWriteScope(guardVault, path); blocked && !isSessionScratchpad(path, payload.SessionID) {
				return deny(reason)
			}
		}
	}
	return nil
}

func init() {
	guardCmd.PersistentFlags().StringVar(&guardVault, "vault", defaultVaultPath(), "path to the vault directory (default: $VAULT_PATH or ./vault)")
	guardDoctorCmd.Flags().StringVar(&guardDoctorRoot, "root", ".", "project root containing .claude/settings.json")
	guardCmd.AddCommand(guardCheckToolCmd)
	guardCmd.AddCommand(guardDoctorCmd)
	rootCmd.AddCommand(guardCmd)
}

func defaultVaultPath() string {
	if v := os.Getenv("VAULT_PATH"); v != "" {
		return v
	}
	// Project scaffolds keep all Claude-owned state together. Retain the
	// legacy ./vault fallback for existing installations and this repository.
	if info, err := os.Stat(filepath.Join(".claude", "vault")); err == nil && info.IsDir() {
		return filepath.Join(".claude", "vault")
	}
	return "vault"
}
