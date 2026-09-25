package orchestrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// The agent runs shell commands (go test executes arbitrary code), so it must
// not inherit whatever credentials happen to be in the caller's environment.
func TestAgentEnvIsAnAllowListNotADenyList(t *testing.T) {
	in := []string{
		"AWS_SECRET_ACCESS_KEY=s3", "AWS_ACCESS_KEY_ID=a", "GH_TOKEN=g", "GITHUB_TOKEN=g2", "NPM_TOKEN=n",
		"GOOGLE_APPLICATION_CREDENTIALS=/creds.json", "DATABASE_URL=postgres://u:p@h/d", "OPENAI_API_KEY=o",
		"SSH_AUTH_SOCK=/agent.sock", "PATH=/bin", "HOME=/h", "LANG=en_US.UTF-8", "LC_ALL=C", "TERM=xterm",
		"TMPDIR=/t", "USER=me", "ANTHROPIC_API_KEY=k", "CLAUDE_CONFIG_DIR=/c", "XDG_CONFIG_HOME=/x",
		"HTTPS_PROXY=http://p", "https_proxy=http://p", "NO_PROXY=x", "GOPATH=/go", "GOFLAGS=-mod=mod", "GOCACHE=/gc",
		"SSL_CERT_FILE=/ca.pem",
	}
	got := envMap(agentEnv(in, "/db", "developer", nil))

	for _, secret := range []string{
		"AWS_SECRET_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "GH_TOKEN", "GITHUB_TOKEN", "NPM_TOKEN",
		"GOOGLE_APPLICATION_CREDENTIALS", "DATABASE_URL", "OPENAI_API_KEY", "SSH_AUTH_SOCK",
	} {
		if _, leaked := got[secret]; leaked {
			t.Errorf("%s reached the agent", secret)
		}
	}
	for _, need := range []string{
		"PATH", "HOME", "LANG", "LC_ALL", "TERM", "TMPDIR", "USER", "ANTHROPIC_API_KEY", "CLAUDE_CONFIG_DIR",
		"XDG_CONFIG_HOME", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "GOPATH", "GOFLAGS", "GOCACHE", "SSL_CERT_FILE",
	} {
		if _, ok := got[need]; !ok {
			t.Errorf("%s was dropped but the agent needs it to run", need)
		}
	}
	if got["ACLINE_ACTOR_TYPE"] != "agent" || got["ACLINE_DB"] != "/db" || got["ACLINE_ROLE"] != "developer" {
		t.Errorf("identity vars not forced: %v", got)
	}
}

func TestAgentEnvPassEnvAddsNamedVariablesButNeverIdentityOrTheToken(t *testing.T) {
	in := []string{"AWS_PROFILE=dev", "NPM_TOKEN=n", "ACLINE_APPROVAL_TOKEN=tok", "ACLINE_ACTOR_TYPE=human", "ACLINE_DB=/other"}
	got := envMap(agentEnv(in, "/db", "", []string{"AWS_PROFILE", "ACLINE_APPROVAL_TOKEN", "ACLINE_ACTOR_TYPE", "ACLINE_DB", "NOT_SET"}))
	if got["AWS_PROFILE"] != "dev" {
		t.Errorf("--pass-env AWS_PROFILE not honoured: %v", got)
	}
	if _, ok := got["NPM_TOKEN"]; ok {
		t.Error("a variable that was not named leaked")
	}
	if _, ok := got["ACLINE_APPROVAL_TOKEN"]; ok {
		t.Error("--pass-env must never hand over the approval token")
	}
	if got["ACLINE_ACTOR_TYPE"] != "agent" || got["ACLINE_DB"] != "/db" {
		t.Errorf("--pass-env overrode forced identity: %v", got)
	}
	if _, ok := got["NOT_SET"]; ok {
		t.Error("an unset variable appeared")
	}
}

func writeSettings(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude", name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var guardTools = []string{"Bash", "Edit", "Glob", "Grep", "NotebookEdit", "Read", "Write"}

const fullHook = `{"hooks":{"PreToolUse":[{"matcher":"Read|Edit|Write|Grep|Glob|Bash|NotebookEdit","hooks":[{"type":"command","command":"python3 \"$CLAUDE_PROJECT_DIR/.claude/hooks/pre_tool_use.py\""}]}]}}`

// Without the guard hook the agent's Read/Edit/Bash calls are not checked for
// secret files, protected files or write scope; the orchestrator must not launch
// into a project that lacks it.
func TestPreflightHooksRefusesAProjectWithoutTheGuard(t *testing.T) {
	cases := []struct {
		name, settings, wantErr string
	}{
		{"no settings file", "", "acline init"},
		{"no PreToolUse hook", `{"hooks":{}}`, "no PreToolUse"},
		{"matcher misses tools", strings.Replace(fullHook, "Read|Edit|Write|Grep|Glob|Bash|NotebookEdit", "Edit|Write", 1), "Bash"},
		{"hook does not call the guard", strings.Replace(fullHook, "pre_tool_use.py", "something_else.py", 1), "guard"},
		{"invalid json", `{not json`, "parsing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.settings != "" {
				writeSettings(t, dir, "settings.json", c.settings)
			}
			err := PreflightHooks(dir, guardTools)
			if err == nil {
				t.Fatal("launch allowed without a working guard hook")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("error %q should mention %q", err, c.wantErr)
			}
		})
	}
}

func TestPreflightHooksAcceptsFullCoverage(t *testing.T) {
	dir := t.TempDir()
	writeSettings(t, dir, "settings.json", fullHook)
	if err := PreflightHooks(dir, guardTools); err != nil {
		t.Fatalf("full coverage refused: %v", err)
	}
	// what `acline init` ships now: the acline binary itself, failing closed
	direct := t.TempDir()
	writeSettings(t, direct, "settings.json", `{"hooks":{"PreToolUse":[{"matcher":"Read|Edit|Write|Grep|Glob|Bash|NotebookEdit","hooks":[{"type":"command","command":"acline hook pre-tool-use || exit 2"}]}]}}`)
	if err := PreflightHooks(direct, guardTools); err != nil {
		t.Fatalf("`acline hook pre-tool-use` refused: %v", err)
	}
	// the guard's own commands count, in any of their forms, and coverage may be
	// split across the shared and the local settings files
	split := t.TempDir()
	writeSettings(t, split, "settings.json", `{"hooks":{"PreToolUse":[{"matcher":"Read|Edit|Write|Grep","hooks":[{"type":"command","command":"acline guard check-tool"}]}]}}`)
	writeSettings(t, split, "settings.local.json", `{"hooks":{"PreToolUse":[{"matcher":"Glob|Bash|NotebookEdit","hooks":[{"type":"command","command":"acline guard check-tool"}]}]}}`)
	if err := PreflightHooks(split, guardTools); err != nil {
		t.Fatalf("split coverage refused: %v", err)
	}
}
