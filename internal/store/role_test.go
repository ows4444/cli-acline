package store

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAssignRoleTracksHandoff covers acline spec #2's workflow-stage
// mechanism: AssignRole sets the task's current role and returns the
// previous one, so the caller (cmd/task.go's `task assign`) can log a
// meaningful old->new role_assigned event -- the assignment history IS the
// workflow trail, with nothing else enforced.
func TestAssignRoleTracksHandoff(t *testing.T) {
	s := humanStore(t)
	taskID, err := s.AddTask("a task", "", "normal", TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}

	designer, err := s.GetRoleByName(nil, "designer")
	if err != nil {
		t.Fatal(err)
	}
	prev, err := s.AssignRole(taskID, &designer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Valid {
		t.Fatalf("expected no previous role on first assignment, got %+v", prev)
	}

	task, err := s.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !task.RoleID.Valid || task.RoleID.Int64 != designer.ID {
		t.Fatalf("expected task.role_id = designer (%d), got %+v", designer.ID, task.RoleID)
	}

	developer, err := s.GetRoleByName(nil, "developer")
	if err != nil {
		t.Fatal(err)
	}
	prev2, err := s.AssignRole(taskID, &developer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !prev2.Valid || prev2.Int64 != designer.ID {
		t.Fatalf("expected the previous role to be designer (%d), got %+v", designer.ID, prev2)
	}

	// Unassign (roleID = nil) must clear it, not error.
	prev3, err := s.AssignRole(taskID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !prev3.Valid || prev3.Int64 != developer.ID {
		t.Fatalf("expected the previous role to be developer (%d), got %+v", developer.ID, prev3)
	}
	task, err = s.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.RoleID.Valid {
		t.Fatalf("expected role_id to be cleared, got %+v", task.RoleID)
	}
}

// TestNextRoleHintIsAdvisoryOnly covers the stage_order-based "next role"
// suggestion: it follows the global pipeline (designer -> developer -> qa
// -> manager), returns nil past the last stage or for an unset/stageless
// role, and never blocks or errors -- it's a hint task show/dashboard can
// choose to print, nothing `task assign` enforces.
func TestNextRoleHintIsAdvisoryOnly(t *testing.T) {
	s := humanStore(t)

	designer, err := s.GetRoleByName(nil, "designer")
	if err != nil {
		t.Fatal(err)
	}
	developer, err := s.GetRoleByName(nil, "developer")
	if err != nil {
		t.Fatal(err)
	}
	qa, err := s.GetRoleByName(nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := s.GetRoleByName(nil, "manager")
	if err != nil {
		t.Fatal(err)
	}
	scrummaster, err := s.GetRoleByName(nil, "scrummaster")
	if err != nil {
		t.Fatal(err)
	}

	valid := func(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }
	cases := []struct {
		name    string
		current sql.NullInt64
		want    string // "" means nil
	}{
		{"designer -> developer", valid(designer.ID), developer.Name},
		{"developer -> qa", valid(developer.ID), qa.Name},
		{"qa -> manager", valid(qa.ID), manager.Name},
		{"manager is last stage", valid(manager.ID), ""},
		{"scrummaster has no stage_order", valid(scrummaster.ID), ""},
		{"unset role_id", sql.NullInt64{}, ""},
	}
	for _, c := range cases {
		hint, err := s.NextRoleHint(nil, c.current)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got := ""
		if hint != nil {
			got = hint.Name
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestRenderContextRoleSection is the roles feature's vault/context piece
// (acline spec #2): renderVaultProse folds in a role's persona file only
// when a role is actually active for this invocation, and leaves the
// output byte-identical to before roles existed otherwise.
func TestRenderContextRoleSection(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "SOUL.md"), []byte("identity prose"), 0o644); err != nil {
		t.Fatal(err)
	}
	rolesDir := filepath.Join(vault, "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolesDir, "qa.md"), []byte("qa persona prose"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := humanStore(t)

	var noRole bytes.Buffer
	if err := s.RenderContext(&noRole, "T", nil, vault); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(noRole.String(), "## Role:") {
		t.Errorf("expected no Role section with no active role, got:\n%s", noRole.String())
	}

	t.Setenv("ACLINE_ROLE", "qa")
	var withRole bytes.Buffer
	if err := s.RenderContext(&withRole, "T", nil, vault); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withRole.String(), "## Role: qa") || !strings.Contains(withRole.String(), "qa persona prose") {
		t.Errorf("expected a Role: qa section with the persona file's contents, got:\n%s", withRole.String())
	}

	t.Setenv("ACLINE_ROLE", "designer") // exists globally, but has no persona file in this vault
	var missingFile bytes.Buffer
	if err := s.RenderContext(&missingFile, "T", nil, vault); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(missingFile.String(), "## Role:") {
		t.Errorf("expected no Role section when the role has no persona file, got:\n%s", missingFile.String())
	}
}
