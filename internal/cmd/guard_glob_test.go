package cmd

import (
	"strings"
	"testing"
)

// The guard looked only at a Grep call's path, so
// Grep{path: ".", glob: "**/.env*", output_mode: "content"} printed the secrets.

func TestSecretGlob(t *testing.T) {
	secret := []string{
		".env", ".env*", "**/.env*", "**/.env.*", "*.env", "config/.env", "{.env,.env.local}", "*.{pem,key}",
		"**/id_rsa", "id_*", "**/*.pem", "*.p12", "**/credentials.json", "**/secrets.y*ml", ".npmrc",
		"**/.ssh/**", ".aws/*", "*.tfstate",
	}
	for _, g := range secret {
		if !secretGlob(g) {
			t.Errorf("secretGlob(%q) = false, want true", g)
		}
	}
	// Broad globs behave like no glob; the Read(...) deny rules cover those.
	ordinary := []string{"*", "**/*", ".*", "*.json", "*.go", "**/*.ts", "src/**/*.{js,jsx}", "*.md", "Makefile", "**/*_test.go", "*.json", "docs/*.md"}
	for _, g := range ordinary {
		if secretGlob(g) {
			t.Errorf("secretGlob(%q) = true, want false", g)
		}
	}
}

func TestGuardCheckToolDeniesAGrepGlobThatReachesSecrets(t *testing.T) {
	withTestStore(t)
	for _, payload := range []string{
		`{"tool_name":"Grep","tool_input":{"pattern":".","path":".","glob":"**/.env*","output_mode":"content"}}`,
		`{"tool_name":"Grep","tool_input":{"pattern":"KEY","glob":"*.pem"}}`,
	} {
		if out := runGuardCheckTool(t, payload); !strings.Contains(out, `"permissionDecision":"deny"`) {
			t.Errorf("expected deny for %s, got %q", payload, out)
		}
	}
	if out := runGuardCheckTool(t, `{"tool_name":"Grep","tool_input":{"pattern":"func main","path":".","glob":"**/*.go","output_mode":"content"}}`); strings.TrimSpace(out) != "" {
		t.Errorf("an ordinary glob was denied: %q", out)
	}
}

func TestBashGrepIncludeGlobsReachingSecretsAreDenied(t *testing.T) {
	blocked := []string{
		`grep -r --include='.env*' KEY .`,
		`grep -rn --include=*.pem BEGIN .`,
		`rg -g '.env*' TOKEN`,
		`rg --glob '**/.env.*' TOKEN`,
		`rg --glob=*.key PRIVATE`,
	}
	for _, c := range blocked {
		if b, _ := checkBashCommandWithDB(c, ""); !b {
			t.Errorf("expected BLOCK for %q", c)
		}
	}
	allowed := []string{`grep -rn --include='*.go' func .`, `rg -g '*.ts' useState`, `grep -r TODO internal`}
	for _, c := range allowed {
		if b, why := checkBashCommandWithDB(c, ""); b {
			t.Errorf("expected ALLOW for %q, got %s", c, why)
		}
	}
}
