package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyPathsAreNormalized(t *testing.T) {
	root := "/proj"
	deny := Policy{DenyPaths: []string{"secrets/*"}}
	for _, p := range []string{"secrets/a.txt", "/proj/secrets/a.txt", "./secrets/a.txt", "secrets/../secrets/a.txt", "/proj/x/../secrets/a.txt"} {
		if ok, _ := deny.CheckIn(root, "Write", p); ok {
			t.Errorf("deny secrets/* allowed %q", p)
		}
	}
	if ok, why := deny.CheckIn(root, "Write", "/proj/src/a.txt"); !ok {
		t.Errorf("unrelated path denied: %s", why)
	}

	allow := Policy{AllowPaths: []string{"internal/*"}}
	for _, p := range []string{"internal/x.go", "/proj/internal/x.go", "internal/store/x.go"} {
		if ok, why := allow.CheckIn(root, "Write", p); !ok {
			t.Errorf("allow internal/* denied %q: %s", p, why)
		}
	}
	for _, p := range []string{"internal/../etc/passwd", "/etc/passwd", "/proj/cmd/x.go", "/proj/internal/../cmd/x.go"} {
		if ok, _ := allow.CheckIn(root, "Write", p); ok {
			t.Errorf("allow internal/* permitted %q", p)
		}
	}
}

func TestPolicyLeadingStarAndDirectoryPatterns(t *testing.T) {
	root := "/proj"
	if ok, _ := (Policy{DenyPaths: []string{"*.env"}}).CheckIn(root, "Read", "/elsewhere/deep/prod.env"); ok {
		t.Error("*.env should match anywhere")
	}
	if ok, _ := (Policy{DenyPaths: []string{"vendor/"}}).CheckIn(root, "Write", "/proj/vendor/a/b.go"); ok {
		t.Error("trailing-slash pattern should cover the directory tree")
	}
	if ok, _ := (Policy{AllowPaths: []string{"*"}}).CheckIn(root, "Write", "/anywhere/x"); !ok {
		t.Error("allow * should allow everything")
	}
}

// Paths were compared as strings, so a symlink inside an allowed directory
// that points outside it satisfied allow_paths while the OS followed the link.
func TestPolicyPathsFollowSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "internal", "escape")); err != nil {
		t.Fatal(err)
	}
	p := Policy{AllowPaths: []string{"internal/*"}}
	if ok, _ := p.CheckIn(root, "Write", "internal/real.go"); !ok {
		t.Fatal("an ordinary allowed path was denied")
	}
	if ok, _ := p.CheckIn(root, "Write", "internal/escape/pwned.go"); ok {
		t.Fatal("a symlink out of the allowed directory satisfied allow_paths")
	}

	// A deny rule still holds for a path reached through a symlink, whichever
	// side of the link it names.
	vendor := t.TempDir()
	if err := os.Symlink(vendor, filepath.Join(root, "vendor")); err != nil {
		t.Fatal(err)
	}
	d := Policy{DenyPaths: []string{"vendor/"}}
	if ok, _ := d.CheckIn(root, "Write", "vendor/x.go"); ok {
		t.Fatal("deny rule on a symlinked directory was bypassed")
	}
	d2 := Policy{DenyPaths: []string{filepath.Join(vendor, "*")}}
	if ok, _ := d2.CheckIn(root, "Write", "vendor/x.go"); ok {
		t.Fatal("deny rule on the link's target was bypassed through the link")
	}
}
