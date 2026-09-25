package scaffold_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"acline/internal/scaffold"
	"acline/internal/store"
)

// The store seeds the roles; the scaffold ships a persona file for each and an
// agent file for each that an agent may take. These three lists were kept in step
// by hand, so this fails when one of them changes without the others.
func TestSeededRolesPersonasAndAgentsAgree(t *testing.T) {
	root := t.TempDir()
	if _, err := scaffold.Write(root); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	roles, err := s.ListRoles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) == 0 {
		t.Fatal("the store seeded no roles")
	}

	seeded := map[string]string{} // name -> kind
	for _, r := range roles {
		seeded[r.Name] = r.Kind
		persona := filepath.Join(root, ".claude", "vault", "roles", r.Name+".md")
		if _, err := os.Stat(persona); err != nil {
			t.Errorf("role %q has no persona file: %v", r.Name, err)
		}
		if _, ok := scaffold.RoleContract(r.Name); !ok {
			t.Errorf("RoleContract(%q) does not resolve", r.Name)
		}
		agent := filepath.Join(root, ".claude", "agents", r.Name+".md")
		_, statErr := os.Stat(agent)
		switch {
		case r.Kind == "human" && statErr == nil:
			t.Errorf("human role %q has an agent file: an agent must not adopt a human role", r.Name)
		case r.Kind != "human" && statErr != nil:
			t.Errorf("%s role %q has no agent file", r.Kind, r.Name)
		}
	}

	for _, dir := range []string{"vault/roles", "agents"} {
		entries, err := os.ReadDir(filepath.Join(root, ".claude", filepath.FromSlash(dir)))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if _, ok := seeded[name]; !ok {
				t.Errorf("%s/%s does not correspond to a seeded role", dir, e.Name())
			}
		}
	}

	// Each agent names itself, and binds to its own persona file.
	frontName := regexp.MustCompile(`(?m)^name: (\S+)$`)
	personaRef := regexp.MustCompile(`\.claude/vault/roles/([a-z]+)\.md`)
	for name, kind := range seeded {
		if kind == "human" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, ".claude", "agents", name+".md"))
		if err != nil {
			continue // already reported above
		}
		body := string(b)
		if m := frontName.FindStringSubmatch(body); m == nil || m[1] != name {
			t.Errorf("agents/%s.md: front-matter name is %v, want %q", name, m, name)
		}
		refs := personaRef.FindAllStringSubmatch(body, -1)
		if len(refs) == 0 {
			t.Errorf("agents/%s.md does not bind to .claude/vault/roles/%s.md", name, name)
		}
		for _, m := range refs { // every reference, not just one: a stray mention of another role is a bug
			if m[1] != name {
				t.Errorf("agents/%s.md refers to .claude/vault/roles/%s.md; an agent binds only to its own persona", name, m[1])
			}
		}
	}
}
