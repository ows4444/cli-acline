package app

import (
	"path/filepath"
	"testing"

	"acline/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestResolveProjectExplicitName(t *testing.T) {
	st := openTestStore(t)
	id, err := st.AddProject("demo", t.TempDir(), "hotl")
	if err != nil {
		t.Fatalf("add project: %v", err)
	}

	got, err := ResolveProject(st, "demo", true)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got == nil || *got != id {
		t.Fatalf("got %v, want %d", got, id)
	}
}

func TestResolveProjectUnknownNameErrors(t *testing.T) {
	st := openTestStore(t)
	if _, err := ResolveProject(st, "nope", true); err == nil {
		t.Fatal("expected an error for an unregistered project name")
	}
}

func TestResolveProjectEmptyWithoutCwdFallbackIsUnscoped(t *testing.T) {
	st := openTestStore(t)
	got, err := ResolveProject(st, "", false)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil (no cwd fallback for MCP-style callers)", got)
	}
}

func TestResolveProjectEmptyWithCwdFallbackFallsBackToNilWhenNoneRegistered(t *testing.T) {
	st := openTestStore(t)
	got, err := ResolveProject(st, "", true)
	if err != nil {
		t.Fatalf("ResolveProject: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil (ErrNoProject collapses to unscoped)", got)
	}
}

func TestResolveProjectOptionalIgnoresAmbientProject(t *testing.T) {
	st := openTestStore(t)
	got, err := ResolveProjectOptional(st, "")
	if err != nil {
		t.Fatalf("ResolveProjectOptional: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestResolveRoleUsesResolvedProjectScope(t *testing.T) {
	st := openTestStore(t)
	projectID, err := st.AddProject("demo", t.TempDir(), "hotl")
	if err != nil {
		t.Fatalf("add project: %v", err)
	}
	roleID, err := st.AddRole(projectID, "signer", "human", false, nil, "")
	if err != nil {
		t.Fatalf("add role: %v", err)
	}

	got, err := ResolveRole(st, "signer", "demo", false)
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if got == nil || *got != roleID {
		t.Fatalf("got %v, want %d", got, roleID)
	}
}
