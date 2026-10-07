//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

// The whole-store snapshot and the audit export were written 0644 while
// the store itself is 0600.
func TestSnapshotAndAuditExportsArePrivate(t *testing.T) {
	c := newTestCLI(t)
	dir := t.TempDir()
	snap := filepath.Join(dir, "snap.json")
	if err := os.WriteFile(snap, []byte("old"), 0o644); err != nil { // an existing, wider file
		t.Fatal(err)
	}
	exportOut := filepath.Join(dir, "audit.jsonl")

	captureStdout(t, func() {
		if err := c.run("snapshot", "export", "--out", snap); err != nil {
			t.Fatal(err)
		}
		if err := c.run("export", "--out", exportOut); err != nil {
			t.Fatal(err)
		}
	})
	for _, p := range []string{snap, exportOut} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s has mode %o, want 600", filepath.Base(p), mode)
		}
	}
}
