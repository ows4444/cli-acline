package cmd

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"acline/internal/store"
)

// The security/qa/architect/designer contracts say "no Edit/Write by design", but
// the persona's tool list only reaches Claude Code's own agent picker. The guard
// enforces it: a session whose role has no Edit/Write cannot write files, through
// the file tools or through Bash.

func startRoleSession(t *testing.T, role string) {
	t.Helper()
	t.Setenv("ACLINE_ROLE", "")
	withTestStore(t)
	if role == "" {
		return
	}
	r, err := st.GetRoleByName(nil, role)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession(nil, nil, &r.ID, ""); err != nil {
		t.Fatal(err)
	}
}

func TestReadOnlyRolesCannotUseTheFileWriteTools(t *testing.T) {
	for _, role := range []string{"security", "qa", "architect", "designer"} {
		startRoleSession(t, role)
		for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
			if blocked, why := checkRoleScope(tool, map[string]any{"file_path": "/x/y.go"}); !blocked || !strings.Contains(why, role) {
				t.Errorf("role %s: %s = (%v, %q), want blocked with the role named", role, tool, blocked, why)
			}
		}
		if blocked, why := checkRoleScope("Read", map[string]any{"file_path": "/x/y.go"}); blocked {
			t.Errorf("role %s: Read blocked: %s", role, why)
		}
	}
}

func TestReadOnlyRoleBashWrites(t *testing.T) {
	startRoleSession(t, "security")
	blocked := []string{
		"sed -i s/a/b/ main.go", "sed -i.bak s/a/b/ main.go", "perl -pi -e 's/a/b/' main.go", "echo pwned > main.go", "echo more >> main.go",
		"cat a | tee out.txt", "rm main.go", "mv a b", "cp a b", "touch new.go", "mkdir out", "truncate -s 0 main.go", "ln -s a b",
		"go test ./... && echo done > result.txt", `python3 -c "open('x','w').write('y')"`, `node -e "require('fs').writeFileSync('x','y')"`,
		"install -m 755 a /usr/local/bin/a", "patch -p1 < fix.diff", "dd if=/dev/zero of=x",
	}
	for _, c := range blocked {
		if b, _ := checkRoleScope("Bash", map[string]any{"command": c}); !b {
			t.Errorf("read-only role: expected BLOCK for %q", c)
		}
	}
	allowed := []string{
		"go test ./...", "go vet ./... 2>/dev/null", "acline check run 1 --kind sast", "acline check record 1 --kind lint --status pass",
		"git diff", "git log --oneline", "cat main.go | grep TODO", "ls > /dev/null", "grep -rn secret . 2>&1", `echo "a > b is a comparison"`,
		"gosec ./... 2>&1 | head", "sed -n 1,20p main.go", "find . -name '*.go' -print", "go build ./... 2>&1",
	}
	for _, c := range allowed {
		if b, why := checkRoleScope("Bash", map[string]any{"command": c}); b {
			t.Errorf("read-only role: expected ALLOW for %q, got %q", c, why)
		}
	}
}

func TestRolesThatMayWriteAreNotRestricted(t *testing.T) {
	for _, role := range []string{"developer", "manager", "scrummaster", ""} {
		startRoleSession(t, role)
		for _, tool := range []string{"Edit", "Write"} {
			if b, why := checkRoleScope(tool, map[string]any{"file_path": "/x/y.go"}); b {
				t.Errorf("role %q: %s blocked: %s", role, tool, why)
			}
		}
		if b, why := checkRoleScope("Bash", map[string]any{"command": "echo x > y.go"}); b {
			t.Errorf("role %q: a redirect was blocked: %s", role, why)
		}
	}
}

func TestRoleFromTheEnvironmentAppliesToo(t *testing.T) {
	startRoleSession(t, "")
	t.Setenv("ACLINE_ROLE", "qa")
	if b, _ := checkRoleScope("Write", map[string]any{"file_path": "/x"}); !b {
		t.Error("ACLINE_ROLE=qa should restrict writes")
	}
	t.Setenv("ACLINE_ROLE", "no-such-role")
	if b, _ := checkRoleScope("Write", map[string]any{"file_path": "/x"}); b {
		t.Error("an unknown role must not block (it is not a role acline ships a contract for)")
	}
}

func TestGuardCheckToolEnforcesTheRoleEndToEnd(t *testing.T) {
	startRoleSession(t, "security")
	out := runGuardCheckTool(t, `{"tool_name":"Bash","tool_input":{"command":"sed -i s/a/b/ main.go"}}`)
	if !strings.Contains(out, `"permissionDecision":"deny"`) || !strings.Contains(out, "security") {
		t.Fatalf("expected a role-based deny, got %q", out)
	}
	events, _ := st.ListEvents(nil, 10)
	found := false
	for _, e := range events {
		if e.Type == "guard_denied" && strings.Contains(e.Message, "security") {
			found = true
		}
	}
	if !found {
		t.Errorf("the denial should be audited as guard_denied: %+v", events)
	}
	if out := runGuardCheckTool(t, `{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`); out != "" {
		t.Errorf("go test must stay allowed for a read-only role, got %q", out)
	}
}

// Session deny-lists only bite for tools the hook is invoked for. The matcher now
// covers the web and MCP tools, so a policy can deny them.
func TestSessionPolicyCanDenyWebAndMcpTools(t *testing.T) {
	withTestStore(t)
	pol, err := (store.Policy{DenyTools: []string{"WebFetch", "mcp__acline__acline_task_done"}}).JSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.StartSession(nil, nil, nil, pol); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"WebFetch", "mcp__acline__acline_task_done"} {
		out := runGuardCheckTool(t, `{"tool_name":"`+tool+`","tool_input":{"url":"https://example.com"}}`)
		if !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("%s should be denied by the session policy, got %q", tool, out)
		}
	}
	if out := runGuardCheckTool(t, `{"tool_name":"WebSearch","tool_input":{"query":"x"}}`); out != "" {
		t.Errorf("a tool the policy does not deny stays allowed, got %q", out)
	}
}

func TestEmbeddedMatcherCoversThePolicyOnlyTools(t *testing.T) {
	data := readScaffoldSettings(t)
	if missing := missingPolicyTools(data); len(missing) > 0 {
		t.Errorf("the scaffolded PreToolUse matcher %q does not include %v, so a session policy cannot deny them", data, missing)
	}
}

func readScaffoldSettings(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../scaffold/assets/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil || len(cfg.Hooks.PreToolUse) == 0 {
		t.Fatalf("parsing scaffold settings: %v", err)
	}
	return cfg.Hooks.PreToolUse[0].Matcher
}

// Ending the session was the way out of a read-only role.
func TestReadOnlyRoleCannotEndOrRestartItsSessionThroughBash(t *testing.T) {
	startRoleSession(t, "security")
	for _, c := range []string{"acline session end -m done", "acline session end && acline session start --role developer", "cd /p && acline --db x.db session start --role developer"} {
		if b, why := checkRoleScope("Bash", map[string]any{"command": c}); !b || !strings.Contains(why, "read-only") {
			t.Errorf("expected BLOCK for %q, got (%v, %q)", c, b, why)
		}
	}
	for _, c := range []string{"acline session current", `acline note add "ask the user to end the session"`} {
		if b, why := checkRoleScope("Bash", map[string]any{"command": c}); b {
			t.Errorf("expected ALLOW for %q, got %q", c, why)
		}
	}
	startRoleSession(t, "developer")
	if b, why := checkRoleScope("Bash", map[string]any{"command": "acline session end -m done"}); b {
		t.Errorf("a writable role may end its session, got %q", why)
	}
}

// Common writers the read-only role check missed.
func TestReadOnlyRoleCatchesCommonIndirectWriters(t *testing.T) {
	startRoleSession(t, "qa")
	blocked := []string{
		"git checkout -- main.go", "git restore main.go", "git apply fix.diff", "git stash", "git commit -am wip",
		"git reset HEAD~1", "git -C sub checkout main",
		"gofmt -w .", "goimports -w main.go", "prettier --write src", "npx prettier -w .", "eslint --fix src",
		"ruff format .", "ruff check --fix .", "black .", "cargo fmt", "go generate ./...", "go mod tidy", "go fmt ./...",
		"tar -xzf a.tgz", "tar xzf a.tgz", "tar --extract -f a.tar", "unzip a.zip", "curl -o out.html https://x", "curl -O https://x/f",
		"wget https://x/f", "npm install", "npm run build", "yarn add left-pad", "pnpm i", "make", "make build",
	}
	for _, c := range blocked {
		if b, _ := checkRoleScope("Bash", map[string]any{"command": c}); !b {
			t.Errorf("read-only role: expected BLOCK for %q", c)
		}
	}
	allowed := []string{
		"git status", "git diff HEAD~1", "git log -p", "git show HEAD:main.go", "git blame main.go",
		"gofmt -l .", "prettier --check src", "eslint src", "ruff check .", "go mod graph", "go list ./...",
		"tar -tzf a.tgz", "curl -s https://x", "npm test", "npm ls", "cargo test",
	}
	for _, c := range allowed {
		if b, why := checkRoleScope("Bash", map[string]any{"command": c}); b {
			t.Errorf("read-only role: expected ALLOW for %q, got %q", c, why)
		}
	}
}
