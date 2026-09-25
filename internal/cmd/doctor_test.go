package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/scaffold"
	"acline/internal/store"
)

func doctorOutput(t *testing.T) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	fails := runDoctor(&buf)
	return buf.String(), fails
}

func TestDoctorOnAHealthyStoreHasNoFailures(t *testing.T) {
	withTestStore(t)
	t.Chdir(t.TempDir())
	out, fails := doctorOutput(t)
	if fails != 0 {
		t.Fatalf("a fresh store failed the doctor:\n%s", out)
	}
	for _, want := range []string{"store", "integrity", "audit trail", "approval token", "guard hooks", "claude", "project"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q line in:\n%s", want, out)
		}
	}
	// a fresh store has no token: that is a warning, not a failure, and it says how to fix it
	if !strings.Contains(out, "[warn]") || !strings.Contains(out, "acline auth init") {
		t.Errorf("expected a warning about the missing approval token:\n%s", out)
	}
}

func TestDoctorFailsOnATamperedAuditTrail(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	s.LogEvent(nil, nil, "note", "one")
	s.LogEvent(nil, nil, "note", "two")
	if _, err := s.DB.Exec(`DROP TRIGGER events_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE events SET message = 'rewritten' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	out, fails := doctorOutput(t)
	if fails == 0 || !strings.Contains(out, "[fail]") || !strings.Contains(out, "audit trail") {
		t.Fatalf("a rewritten event went unnoticed (fails=%d):\n%s", fails, out)
	}
}

func TestDoctorFailsOnAForgedApproval(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	id, _ := s.AddTask("t", "", "normal", store.TaskOpts{})
	s.AddCheck(id, "test", "pass", "")
	s.DB.Exec(`INSERT INTO approvals (id, task_id, kind, approver, decision, actor_type, actor_id, created_at)
		VALUES (9001, ?, 'code_review', 'alice', 'approved', 'human', 'alice', '2026-01-01T00:00:00Z')`, id)
	out, fails := doctorOutput(t)
	if fails == 0 || !strings.Contains(out, "approvals#9001") {
		t.Fatalf("a forged approval went unnoticed (fails=%d):\n%s", fails, out)
	}
}

func TestDoctorReportsGuardHooksForTheCurrentProject(t *testing.T) {
	withTestStore(t)
	dir := t.TempDir()
	t.Chdir(dir)
	out, _ := doctorOutput(t)
	if !strings.Contains(out, "guard hooks") || !strings.Contains(out, "acline init") {
		t.Errorf("a directory with no .claude/settings.json should say how to install the hooks:\n%s", out)
	}
	if _, err := scaffold.Write(dir); err != nil {
		t.Fatal(err)
	}
	out, fails := doctorOutput(t)
	if fails != 0 || !strings.Contains(out, "[ok]   guard hooks") {
		t.Errorf("a scaffolded project should pass the hook check:\n%s", out)
	}
	// weaken the matcher: the doctor names the uncovered tools
	settings := filepath.Join(dir, ".claude", "settings.json")
	data, _ := os.ReadFile(settings)
	os.WriteFile(settings, []byte(strings.ReplaceAll(string(data), "Bash|", "")), 0o644)
	out, _ = doctorOutput(t)
	if !strings.Contains(out, "Bash") || !strings.Contains(out, "[warn]") {
		t.Errorf("an incomplete matcher should be reported:\n%s", out)
	}
}

func TestDoctorListsPreMigrationBackups(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	if err := os.WriteFile(s.Path+".pre-v9", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _ := doctorOutput(t)
	if !strings.Contains(out, ".pre-v9") {
		t.Errorf("backups next to the store should be listed:\n%s", out)
	}
}

func TestDoctorWarnsWhenAMultiProjectStoreResolvesNoProject(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	s.AddProject("a", "/tmp/a-does-not-matter", "hotl")
	s.AddProject("b", "/tmp/b-does-not-matter", "hotl")
	out, _ := doctorOutput(t)
	if !strings.Contains(out, "project") || !strings.Contains(out, "every project") {
		t.Errorf("expected a warning that this directory sees all projects:\n%s", out)
	}
}

// A root registered before registration refused broad paths still widens
// the guard's write scope, so doctor must say so.
func TestDoctorFailsOnAnOverBroadProjectRoot(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	if _, err := s.DB.Exec(`INSERT INTO projects (name, path, autonomy_default, created_at) VALUES ('everything', '/', 'hotl', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	out, fails := doctorOutput(t)
	if fails == 0 || !strings.Contains(out, `project "everything" is registered at /`) {
		t.Errorf("an over-broad root went unreported (fails=%d):\n%s", fails, out)
	}
}

// `acline doctor` suggests runners for a non-Go project; it never sets them.
func TestDoctorSuggestsRunnersForANonGoProject(t *testing.T) {
	s := withTestStore(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)
	t.Chdir(dir)
	pid, err := s.AddProject("web", dir, "hotl")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := doctorOutput(t)
	if !strings.Contains(out, "acline check runner set test npm test") {
		t.Fatalf("no runner suggestion:\n%s", out)
	}
	if runners, _ := s.ListCheckRunners(pid); len(runners) != 0 {
		t.Fatalf("doctor set runners itself: %+v", runners)
	}
	for _, k := range []string{"test", "lint", "sca"} {
		if err := s.SetCheckRunner(pid, k, "true", ""); err != nil {
			t.Fatal(err)
		}
	}
	if out, _ := doctorOutput(t); strings.Contains(out, "acline check runner set") {
		t.Fatalf("suggested runners that are already set:\n%s", out)
	}
}

// An abandoned session blocks new ones in its project and keeps its policy
// in force; doctor reports it rather than ending it.
func TestDoctorReportsAbandonedSessions(t *testing.T) {
	s := withTestStore(t)
	t.Chdir(t.TempDir())
	id, err := s.BeginSession(store.SessionStart{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE sessions SET started_at = '2020-01-01T00:00:00Z' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	out, _ := doctorOutput(t)
	if !strings.Contains(out, fmt.Sprintf("#%d started 2020-01-01", id)) {
		t.Fatalf("abandoned session not reported:\n%s", out)
	}
	if _, err := s.CurrentSession(); err != nil {
		t.Fatal("doctor must not end the session")
	}
}
