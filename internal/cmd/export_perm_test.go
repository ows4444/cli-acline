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
	withTestStore(t)
	dir := t.TempDir()
	snap := filepath.Join(dir, "snap.json")
	if err := os.WriteFile(snap, []byte("old"), 0o644); err != nil { // an existing, wider file
		t.Fatal(err)
	}
	prevSnap, prevOut := snapshotExportPath, exportOut
	snapshotExportPath, exportOut = snap, filepath.Join(dir, "audit.jsonl")
	t.Cleanup(func() { snapshotExportPath, exportOut = prevSnap, prevOut })

	captureStdout(t, func() {
		if err := snapshotExportCmd.RunE(snapshotExportCmd, nil); err != nil {
			t.Fatal(err)
		}
		if err := exportCmd.RunE(exportCmd, nil); err != nil {
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
