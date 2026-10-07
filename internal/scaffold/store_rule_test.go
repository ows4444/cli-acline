package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRuleCoversTheStoreActuallyUsed(t *testing.T) {
	home := filepath.FromSlash("/home/me")
	for _, c := range []struct{ path, want string }{
		{"", "~/.acline/**"},
		{filepath.Join(home, ".acline", "store.db"), "~/.acline/**"},
		{filepath.Join(home, ".local", "share", "acline", "store.db"), "~/.local/share/acline/store.db*"},
		{filepath.FromSlash("/srv/acline/store.db"), "//srv/acline/store.db*"},
		// A store inside a project denies the file, not the project around it.
		{filepath.Join(home, "repo", "acline.db"), "~/repo/acline.db*"},
		{filepath.FromSlash("/home/meme/x.db"), "//home/meme/x.db*"},
	} {
		if got := storeRule(c.path, home); got != c.want {
			t.Errorf("storeRule(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestScaffoldDeniesTheConfiguredStore(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "elsewhere", "store.db")
	if _, err := WriteOpts(root, Options{StorePath: db}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	home, _ := os.UserHomeDir()
	rule := storeRule(db, home)
	if !strings.HasPrefix(rule, "//") && !strings.HasPrefix(rule, "~/") {
		t.Fatalf("rule %q is neither absolute (//) nor home-relative (~/)", rule)
	}
	for _, tool := range []string{"Read", "Edit", "Write"} {
		if !strings.Contains(s, tool+"("+rule+")") {
			t.Errorf("settings miss %s(%s):\n%s", tool, rule, s)
		}
	}
	if strings.Contains(s, "~/.acline/**") {
		t.Errorf("settings still deny the default store instead of %s", db)
	}
}
