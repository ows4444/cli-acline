package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// A newer acline migrates the shared store the first time it opens it, and an
// older one then refuses it. The only way back used to be a backup somebody
// remembered to take.

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Actor = Actor{Type: "human", ID: "t"}
	return s
}

func TestMigratingAnOlderStoreLeavesABackupOfTheOldOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s := openAt(t, path)
	id, _ := s.AddTask("work in progress", "", "normal", TaskOpts{})
	if _, err := s.DB.Exec(`PRAGMA user_version = 9`); err != nil { // as if written by an older acline
		t.Fatal(err)
	}
	s.Close()

	s = openAt(t, path) // migrates 9 -> current
	defer s.Close()
	backup := path + ".pre-v9"
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatalf("no backup was written before migrating: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %v, want 0600 (it holds specs, decisions and the audit trail)", info.Mode().Perm())
	}

	old := openReadOnlyCopy(t, backup)
	defer old.Close()
	var version int
	if err := old.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 9 {
		t.Fatalf("backup is at version %d (%v), want the pre-migration 9", version, err)
	}
	var title string
	if err := old.DB.QueryRow(`SELECT title FROM tasks WHERE id = ?`, id).Scan(&title); err != nil || title != "work in progress" {
		t.Fatalf("backup lost the data: %q, %v", title, err)
	}
}

func openReadOnlyCopy(t *testing.T, path string) *Store {
	t.Helper()
	// Copy first: opening a store at a lower version would migrate it, and the backup must stay untouched.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cp := filepath.Join(t.TempDir(), "copy.db")
	if err := os.WriteFile(cp, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", cp)
	if err != nil {
		t.Fatal(err)
	}
	return &Store{DB: db}
}

func TestNoBackupForAFreshStoreOrWhenNothingIsMigrated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.db")
	s := openAt(t, path)
	s.Close()
	openAt(t, path).Close() // same version: nothing to migrate
	matches, _ := filepath.Glob(path + ".pre-v*")
	if len(matches) != 0 {
		t.Fatalf("unexpected backups: %v", matches)
	}
}

func TestAnExistingBackupIsNeverOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s := openAt(t, path)
	s.AddTask("first", "", "normal", TaskOpts{})
	s.DB.Exec(`PRAGMA user_version = 9`)
	s.Close()
	openAt(t, path).Close() // writes store.db.pre-v9
	first, _ := os.ReadFile(path + ".pre-v9")

	s = openAt(t, path)
	s.AddTask("second", "", "normal", TaskOpts{})
	s.DB.Exec(`PRAGMA user_version = 9`)
	s.Close()
	openAt(t, path).Close() // would write the same name again
	if again, _ := os.ReadFile(path + ".pre-v9"); string(again) != string(first) {
		t.Fatal("the earlier backup was overwritten")
	}
}

func TestAStoreNewerThanThisBuildIsStillRefusedAndNotBackedUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	s := openAt(t, path)
	s.DB.Exec(`PRAGMA user_version = 999`)
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("a newer store was opened")
	}
	if matches, _ := filepath.Glob(path + ".pre-v*"); len(matches) != 0 {
		t.Fatalf("backed up a store that was refused: %v", matches)
	}
}

// Several processes (hooks, mcp serve, the CLI) opening an old store at once
// all tried to back it up and migrate it; the losers failed to open.
func TestConcurrentOpensOfAnOldStoreAllSucceed(t *testing.T) {
	path := t.TempDir() + "/old.db"
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`PRAGMA user_version = 12`); err != nil {
		t.Fatal(err)
	}
	s.Close()

	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, err := Open(path)
			if err == nil {
				st.Close()
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent Open: %v", err)
		}
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	s.DB.QueryRow("PRAGMA user_version").Scan(&v)
	if v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
}
