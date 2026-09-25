package cmd

import (
	"os"
	"os/exec"
	"testing"
)

func TestProjectNameFromGoMod(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("go.mod", []byte("module github.com/example/widget-tool\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, ok := projectNameFromGoMod()
	if !ok || name != "widget-tool" {
		t.Errorf("got (%q, %v), want (%q, true)", name, ok, "widget-tool")
	}
}

func TestProjectNameFromGoModMissingFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, ok := projectNameFromGoMod(); ok {
		t.Error("expected ok=false with no go.mod present")
	}
}

func TestProjectNameFromPackageJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("package.json", []byte(`{"name": "@myorg/widget-app", "version": "1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	name, ok := projectNameFromPackageJSON()
	if !ok || name != "widget-app" {
		t.Errorf("got (%q, %v), want scoped name stripped to %q", name, ok, "widget-app")
	}
}

func TestProjectNameFromPackageJSONUnscoped(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("package.json", []byte(`{"name": "widget-app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	name, ok := projectNameFromPackageJSON()
	if !ok || name != "widget-app" {
		t.Errorf("got (%q, %v), want (%q, true)", name, ok, "widget-app")
	}
}

func TestProjectNameFromPackageJSONMalformed(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("package.json", []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := projectNameFromPackageJSON(); ok {
		t.Error("expected ok=false for malformed package.json")
	}
}

func TestProjectNameFromGitRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", "https://github.com/example/widget-tool.git")

	name, ok := projectNameFromGitRemote()
	if !ok || name != "widget-tool" {
		t.Errorf("got (%q, %v), want (%q, true)", name, ok, "widget-tool")
	}
}

func TestProjectNameFromGitRemoteNoRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if out, err := exec.Command("git", "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v (%s)", err, out)
	}
	if _, ok := projectNameFromGitRemote(); ok {
		t.Error("expected ok=false with no origin remote configured")
	}
}

func TestDetectProjectNameFallsBackToDirName(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No go.mod, no package.json, and (most likely) no git remote in a
	// bare temp dir -- detectProjectName should fall back all the way to
	// the directory name via defaultProjectName.
	if got, want := detectProjectName(), defaultProjectName(); got != want {
		t.Errorf("detectProjectName() = %q, want fallback %q", got, want)
	}
}

func TestDetectProjectNamePrefersGoMod(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("go.mod", []byte("module winner\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("package.json", []byte(`{"name": "loser"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectProjectName(); got != "winner" {
		t.Errorf("detectProjectName() = %q, want go.mod's name to win over package.json", got)
	}
}
