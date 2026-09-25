package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// The markdown under internal/scaffold/assets is prompt text: it is loaded into an
// agent's context, embedded in orchestrator prompts (role contracts), or matched
// by the guard. Prose that names a command that does not exist, or promises what
// the code no longer enforces, misleads an agent on every session. These tests
// hold that text to the CLI and to a small set of rules, so it cannot rot.

const assetsRoot = "../scaffold/assets"

type assetFile struct {
	rel   string // path under assets, slash-separated
	kind  string // soul | user | bootstrap | role | agent | skill
	body  string // whole file
	front map[string]string
	text  string // body after the front matter
}

func loadAssets(t *testing.T) []assetFile {
	t.Helper()
	var out []assetFile
	err := filepath.WalkDir(assetsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		rel, _ := filepath.Rel(assetsRoot, path)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f := assetFile{rel: rel, body: string(b), front: map[string]string{}}
		switch {
		case rel == "vault/SOUL.md":
			f.kind = "soul"
		case rel == "vault/USER.md":
			f.kind = "user"
		case rel == "vault/BOOTSTRAP.md":
			f.kind = "bootstrap"
		case strings.HasPrefix(rel, "vault/roles/"):
			f.kind = "role"
		case strings.HasPrefix(rel, "agents/"):
			f.kind = "agent"
		case strings.HasPrefix(rel, "skills/"):
			f.kind = "skill"
		default:
			t.Fatalf("unclassified asset %s: add it to the rubric", rel)
		}
		rest, ok := strings.CutPrefix(f.body, "---\n")
		if !ok {
			t.Errorf("%s: no front matter", rel)
			f.text = f.body
		} else {
			front, text, _ := strings.Cut(rest, "\n---\n")
			for _, line := range strings.Split(front, "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok {
					f.front[strings.TrimSpace(k)] = strings.TrimSpace(v)
				}
			}
			f.text = text
		}
		out = append(out, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out
}

func words(s string) int { return len(strings.Fields(s)) }

// --- 1. every command and flag the prose mentions exists ---

var codeSpan = regexp.MustCompile("`([^`\n]+)`")

func TestAssetsOnlyMentionRealCommandsAndFlags(t *testing.T) {
	checked := 0
	for _, f := range loadAssets(t) {
		var last *cobra.Command // the command a following flag-only span ("and `--kind lint`") belongs to
		for _, m := range codeSpan.FindAllStringSubmatch(f.text, -1) {
			span := strings.TrimSpace(m[1])
			var cmd *cobra.Command
			switch {
			case strings.HasPrefix(span, "acline "):
				checked++
				var err error
				if cmd, err = resolveInvocation(span); err != nil {
					t.Errorf("%s: `%s`: %v", f.rel, span, err)
					continue
				}
				last = cmd
			case strings.HasPrefix(span, "--") && last != nil:
				cmd = last
			default:
				continue
			}
			for _, flag := range flagsIn(span) {
				if cmd.Flags().Lookup(flag) == nil && cmd.InheritedFlags().Lookup(flag) == nil && cmd.PersistentFlags().Lookup(flag) == nil {
					t.Errorf("%s: `%s`: %q has no flag --%s", f.rel, span, cmd.CommandPath(), flag)
				}
			}
		}
	}
	if checked < 30 {
		t.Fatalf("only %d acline invocations were checked; the extractor is probably broken", checked)
	}
}

// resolveInvocation finds the command a span like `acline task assign <id> qa`
// names, using only its leading command words (a word that starts a placeholder,
// a quote or a flag ends them).
func resolveInvocation(span string) (*cobra.Command, error) {
	var words []string
	for _, tok := range strings.Fields(span)[1:] {
		if !regexp.MustCompile(`^[a-z][a-z-]*$`).MatchString(tok) {
			break
		}
		words = append(words, tok)
	}
	if len(words) == 0 {
		return rootCmd, nil // `acline <something>` or a bare mention
	}
	cmd, rest, err := rootCmd.Find(words)
	if err != nil || cmd == rootCmd {
		return nil, errNoCommand(words)
	}
	// a leftover word must be a positional argument of a leaf command, not a
	// misspelt subcommand of a command that has subcommands
	if len(rest) > 0 && cmd.HasSubCommands() {
		return nil, errNoCommand(words)
	}
	return cmd, nil
}

type errNoCommand []string

func (e errNoCommand) Error() string { return "no such command: acline " + strings.Join(e, " ") }

var flagRe = regexp.MustCompile(`(?:^|\s)--([a-z][a-z-]*)`)

func flagsIn(span string) []string {
	var out []string
	for _, m := range flagRe.FindAllStringSubmatch(span, -1) {
		out = append(out, m[1])
	}
	return out
}

// --- 2. no stale claims ---

// Each of these was true once and is false now; the reason says why.
var staleClaims = map[string]string{
	"--by <person>":                         "an agent can no longer approve by naming a person; it needs the approval token",
	"needs `--by":                           "same",
	"pre_tool_use.py":                       "the hooks are `acline hook <event>` now",
	"python3":                               "there is no Python runtime dependency",
	".claude/hooks":                         "there are no hook scripts any more",
	"vault/daily":                           "the daily folder was removed",
	"vault/research":                        "the research folder was removed",
	"REVIEW.md":                             "no such document",
	"PLANNING.md":                           "no such document",
	"ORCHESTRATOR.md":                       "no such document",
	"no Edit/Write access by design":        "the tool list omits Edit/Write, but Bash is only held to it when the session runs as that role; say exactly that",
	"should not adopt":                      "an agent is refused a human-only role, not merely asked not to adopt it",
	"already enforced at the process level": "stale wording about approvals",
}

func TestAssetsMakeNoStaleClaims(t *testing.T) {
	for _, f := range loadAssets(t) {
		lower := strings.ToLower(f.body)
		for phrase, why := range staleClaims {
			if strings.Contains(lower, strings.ToLower(phrase)) {
				t.Errorf("%s contains %q — %s", f.rel, phrase, why)
			}
		}
	}
}

// --- 3. front matter ---

func TestAssetFrontMatter(t *testing.T) {
	for _, f := range loadAssets(t) {
		var need []string
		switch f.kind {
		case "soul":
			need = []string{"type", "protected"}
		case "user":
			need = []string{"type"}
		case "bootstrap":
			need = []string{"type"}
		case "role":
			need = []string{"type", "role", "protected"}
		case "agent":
			need = []string{"name", "description", "tools"}
		case "skill":
			need = []string{"name", "description", "allowed-tools"}
		}
		for _, k := range need {
			if f.front[k] == "" {
				t.Errorf("%s: front matter has no %q", f.rel, k)
			}
		}
		if f.kind == "role" && f.front["role"] != strings.TrimSuffix(filepath.Base(f.rel), ".md") {
			t.Errorf("%s: role %q does not match the file name", f.rel, f.front["role"])
		}
		if f.kind == "skill" {
			dir := filepath.Base(filepath.Dir(f.rel))
			if f.front["name"] != dir {
				t.Errorf("%s: skill name %q does not match its directory %q", f.rel, f.front["name"], dir)
			}
		}
	}
}

// --- 4. budgets ---

// Files loaded into every session are held tightest: their cost is paid whether
// or not they are relevant.
var wordBudget = map[string]int{"soul": 430, "user": 60, "bootstrap": 230, "role": 190, "agent": 120, "skill": 260}

func TestAssetsStayWithinTheirWordBudgets(t *testing.T) {
	for _, f := range loadAssets(t) {
		if got, max := words(f.text), wordBudget[f.kind]; got > max {
			t.Errorf("%s is %d words, over the %d budget for a %s file", f.rel, got, max, f.kind)
		}
	}
	always := 0
	for _, f := range loadAssets(t) {
		if f.kind == "soul" || f.kind == "user" {
			always += words(f.text)
		}
	}
	if always > 480 {
		t.Errorf("SOUL.md + USER.md are %d words, loaded into every session; keep them under 480", always)
	}
}

// --- 5. agent descriptions are delegation triggers ---

func TestAgentDescriptionsSayWhenToUseThem(t *testing.T) {
	for _, f := range loadAssets(t) {
		if f.kind != "agent" {
			continue
		}
		d := f.front["description"]
		if !strings.HasPrefix(d, "Use ") {
			t.Errorf("%s: description must start with \"Use \" so Claude Code knows when to delegate: %q", f.rel, d)
		}
		if len(d) < 60 || len(d) > 300 {
			t.Errorf("%s: description is %d characters, want 60-300", f.rel, len(d))
		}
		if strings.Contains(strings.ToLower(d), "dispatch target") {
			t.Errorf("%s: description is a label, not a trigger: %q", f.rel, d)
		}
	}
}

// --- 6. read-only agents state their enforcement exactly ---

func TestReadOnlyAgentsDescribeTheirLimitHonestly(t *testing.T) {
	for _, f := range loadAssets(t) {
		if f.kind != "agent" || strings.Contains(f.front["tools"], "Edit") {
			continue
		}
		if !strings.Contains(f.text, "ACLINE_ROLE") || !strings.Contains(f.text, "guard") {
			t.Errorf("%s: a read-only agent must say the guard holds Bash to read-only only when the session runs as this role (ACLINE_ROLE)", f.rel)
		}
	}
}
