package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var mode string
	if err := s.DB.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v; want wal", mode, err)
	}
	var fk int
	if err := s.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v; want 1", fk, err)
	}
	var busy int
	if err := s.DB.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy < 1000 {
		t.Fatalf("busy_timeout = %d, %v; want a non-trivial timeout", busy, err)
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec("PRAGMA user_version = 999"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if s2, err := Open(path); err == nil {
		s2.Close()
		t.Fatal("Open succeeded on a store newer than this build")
	}
}

func TestOpenRejectsQuestionMarkInPath(t *testing.T) {
	if s, err := Open(filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		s.Close()
		t.Fatal("Open accepted a path containing '?'")
	}
}

// Two independently opened stores (as two processes would be) writing
// concurrently must both succeed, and the audit hash chain must stay valid.
func TestConcurrentStoresWriteWithoutBusyErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for _, st := range []*Store{a, b} {
		st := st
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if _, err := st.LogEvent(nil, nil, "note", "concurrent"); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent LogEvent: %v", err)
	}
	res, err := a.VerifyChain()
	if err != nil || !res.OK() {
		t.Fatalf("chain after concurrent writes: %+v, %v", res, err)
	}
	if res.Checked != 41 { // 40 writes plus the hash_version marker
		t.Fatalf("checked %d events, want 41", res.Checked)
	}
}

func TestOpenCreatesPrivateStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "newdir")
	path := filepath.Join(dir, "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(nil, "x", "manual"); err != nil { // forces WAL sidecars to exist
		t.Fatal(err)
	}
	defer s.Close()
	for _, p := range []string{dir, path, path + "-wal"} {
		fi, err := os.Stat(p)
		if err != nil {
			continue // a sidecar may already be checkpointed away
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is accessible to group/other: %v", p, fi.Mode().Perm())
		}
	}
}

func TestOpenLeavesExistingPermissionsAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("existing store mode changed to %v", fi.Mode().Perm())
	}
}
