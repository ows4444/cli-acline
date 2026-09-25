package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "main.go", "package main\n")
	write(t, dir, ".gitignore", "build/\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestHashChangesWhenTheCodeChanges(t *testing.T) {
	dir := newRepo(t)
	base := Hash(dir)
	if !strings.HasPrefix(base, "sha256:") {
		t.Fatalf("Hash = %q", base)
	}
	if again := Hash(dir); again != base {
		t.Fatalf("hash is not stable: %s vs %s", base, again)
	}

	write(t, dir, "main.go", "package main\n// edited\n")
	edited := Hash(dir)
	if edited == base {
		t.Fatal("editing a tracked file did not change the hash")
	}
	write(t, dir, "new.go", "package main\n")
	if withNew := Hash(dir); withNew == edited {
		t.Fatal("an untracked file did not change the hash")
	}
	os.Remove(filepath.Join(dir, "new.go"))
	if back := Hash(dir); back != edited {
		t.Fatal("removing the untracked file should restore the previous hash")
	}
	os.Remove(filepath.Join(dir, "main.go"))
	if deleted := Hash(dir); deleted == edited || deleted == "" {
		t.Fatalf("deleting a tracked file: %q", deleted)
	}
}

func TestHashIgnoresGitIgnoredAndGitInternals(t *testing.T) {
	dir := newRepo(t)
	base := Hash(dir)
	write(t, dir, "build/output.bin", "compiled")
	if Hash(dir) != base {
		t.Error("a gitignored build output changed the hash")
	}
	// a commit of unchanged content does not change what the tree contains
	write(t, dir, "a.txt", "x")
	before := Hash(dir)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "second")
	if Hash(dir) != before {
		t.Error("committing the same content changed the hash: it must describe the tree, not the history")
	}
}

func TestHashWorksOutsideGit(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.txt", "one")
	write(t, dir, "sub/b.txt", "two")
	write(t, dir, "node_modules/x/y.js", "ignored")
	h := Hash(dir)
	if !strings.HasPrefix(h, "sha256:") {
		t.Fatalf("Hash = %q", h)
	}
	write(t, dir, "node_modules/x/y.js", "still ignored, changed")
	if Hash(dir) != h {
		t.Error("node_modules changed the hash")
	}
	write(t, dir, "sub/b.txt", "changed")
	if Hash(dir) == h {
		t.Error("an edit did not change the hash")
	}
}

func TestHashIsTheSameForTheSameContentInAnotherDirectory(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, d := range []string{a, b} {
		write(t, d, "x/y.txt", "same")
		write(t, d, "z.txt", "same too")
	}
	if Hash(a) != Hash(b) {
		t.Error("the hash depends on the directory's location, so it cannot compare a checkout with a CI copy")
	}
}

func TestHashOfAMissingDirectoryIsUnknown(t *testing.T) {
	if got := Hash(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("Hash of a missing dir = %q, want empty (unknown)", got)
	}
	if got := Hash(""); got != "" {
		t.Errorf("Hash of \"\" = %q, want empty (unknown)", got)
	}
}
