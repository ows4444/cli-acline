package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A registered project path is part of the guard's write scope, so an agent
// that could register `/` could write anywhere.

func TestAgentCannotRegisterAProjectPath(t *testing.T) {
	h := humanStore(t)
	a := asAgent(h)
	dir := t.TempDir()
	if _, err := a.AddProject("sneaky", dir, ""); !errors.Is(err, ErrAgentCannotRegisterProjectPath) {
		t.Fatalf("agent registering a path = %v, want ErrAgentCannotRegisterProjectPath", err)
	}
	if ps, _ := h.ListProjects(); len(ps) != 0 {
		t.Fatalf("a refused project was written: %+v", ps)
	}
	// A project with no path widens nothing, so an agent may still add one.
	if _, err := a.AddProject("label-only", "", ""); err != nil {
		t.Fatalf("agent adding a path-less project = %v", err)
	}
	if _, err := h.AddProject("real", dir, ""); err != nil {
		t.Fatalf("person registering a path = %v", err)
	}
}

func TestAgentMayRegisterAProjectPathWithTheToken(t *testing.T) {
	h := humanStore(t)
	tok, err := h.EnableApprovalToken()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := h.AddProject("p", dir, ""); !errors.Is(err, ErrApprovalTokenRequired) {
		t.Fatalf("no token = %v, want ErrApprovalTokenRequired", err)
	}
	if _, err := asAgent(h).AddProjectWithToken("p", dir, "", tok); err != nil {
		t.Fatalf("agent with the token = %v", err)
	}
}

func TestNobodyCanRegisterTheRootOrTheHomeDirectory(t *testing.T) {
	h := humanStore(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, p := range []string{"/", home, filepath.Dir(home), home + "/"} {
		if _, err := h.AddProject("broad", p, ""); !errors.Is(err, ErrProjectPathTooBroad) {
			t.Errorf("registering %q = %v, want ErrProjectPathTooBroad", p, err)
		}
	}
	sub := filepath.Join(home, "code", "repo")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := h.AddProject("repo", sub, ""); err != nil {
		t.Fatalf("a directory inside HOME = %v", err)
	}
}

func TestRegisteringAProjectIsAudited(t *testing.T) {
	h := humanStore(t)
	dir := t.TempDir()
	if _, err := h.AddProject("p", dir, ""); err != nil {
		t.Fatal(err)
	}
	events, _ := h.QueryEvents(EventFilter{Limit: 10})
	for _, e := range events {
		if e.Type == "project_added" {
			return
		}
	}
	t.Fatalf("no project_added event: %+v", events)
}
