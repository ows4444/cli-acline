package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A `.acline-project` file in a repo you did not register could name any project,
// and session_start.py would then inject that project's decisions and memory into
// the session. A marker is only trusted where the project's registered path says
// it may be.

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(parts...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestMarkerOutsideTheProjectsPathIsIgnored(t *testing.T) {
	h := humanStore(t)
	root := t.TempDir()
	pb := mkdir(t, root, "pb")
	stranger := mkdir(t, root, "somebody-elses-repo")
	if _, err := h.AddProject("pb", pb, "hotl"); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(stranger, "pb"); err != nil {
		t.Fatal(err)
	}
	if p, err := h.ResolveProjectForPath(stranger); !errors.Is(err, ErrNoProject) {
		t.Fatalf("a marker naming a project registered elsewhere was trusted: %+v, %v", p, err)
	}
}

func TestMarkerInsideTheProjectsPathIsTrusted(t *testing.T) {
	h := humanStore(t)
	root := t.TempDir()
	pb := mkdir(t, root, "pb")
	sub := mkdir(t, pb, "services", "api")
	if _, err := h.AddProject("pb", pb, "hotl"); err != nil {
		t.Fatal(err)
	}
	for _, where := range []string{pb, filepath.Dir(sub), sub} {
		if err := WriteMarker(where, "pb"); err != nil {
			t.Fatal(err)
		}
		p, err := h.ResolveProjectForPath(sub)
		if err != nil || p.Name != "pb" {
			t.Fatalf("marker at %s: %+v, %v", where, p, err)
		}
		os.Remove(filepath.Join(where, marker))
	}
}

func TestMarkerNamingAProjectWithoutAPathCannotBeChecked(t *testing.T) {
	h := humanStore(t)
	dir := mkdir(t, t.TempDir(), "repo")
	if _, err := h.AddProject("pathless", "", "hotl"); err != nil {
		t.Fatal(err)
	}
	WriteMarker(dir, "pathless")
	if p, err := h.ResolveProjectForPath(dir); err != nil || p.Name != "pathless" {
		t.Fatalf("a path-less project has nothing to compare against, so its marker stays trusted: %+v, %v", p, err)
	}
}

func TestProjectResolvesThroughASymlinkedPath(t *testing.T) {
	h := humanStore(t)
	root := t.TempDir()
	real := mkdir(t, root, "real-repo")
	link := filepath.Join(root, "via-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := h.AddProject("p", real, "hotl"); err != nil {
		t.Fatal(err)
	}
	// cwd reached through a symlink (e.g. /var vs /private/var on macOS)
	if p, err := h.ResolveProjectForPath(filepath.Join(link, "x")); err == nil {
		t.Fatalf("a path that does not exist should not resolve: %+v", p)
	}
	mkdir(t, real, "sub")
	if p, err := h.ResolveProjectForPath(filepath.Join(link, "sub")); err != nil || p.Name != "p" {
		t.Fatalf("via symlink: %+v, %v", p, err)
	}
	// and a project registered through the link matches the real path
	if _, err := h.AddProject("q", filepath.Join(link, "sub"), "hotl"); err != nil {
		t.Fatal(err)
	}
	if p, err := h.ResolveProjectForPath(filepath.Join(real, "sub")); err != nil || p.Name != "q" {
		t.Fatalf("registered via symlink, resolved by real path: %+v, %v", p, err)
	}
	// a marker reached through the link is trusted when it is inside the project
	WriteMarker(real, "p")
	if p, err := h.ResolveProjectForPath(filepath.Join(link)); err != nil || p.Name != "p" {
		t.Fatalf("marker via symlink: %+v, %v", p, err)
	}
}
