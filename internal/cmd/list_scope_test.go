package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"acline/internal/store"
)

// Inside a project, list commands show that project; --all-projects widens
// them, and outside any project they show everything, as before.
func TestListCommandsDefaultToTheCurrentProject(t *testing.T) {
	c := newTestCLI(t)
	for _, args := range [][]string{
		{"project", "add", "proj-a", "/tmp/proj-a"},
		{"project", "add", "proj-b", "/tmp/proj-b"},
		{"task", "add", "--project", "proj-a", "A task"},
		{"task", "add", "--project", "proj-b", "B task"},
		{"spec", "add", "--project", "proj-a", "A spec"},
		{"spec", "add", "--project", "proj-b", "B spec"},
	} {
		if err := c.run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	titles := func(args ...string) []string {
		t.Helper()
		var rows []struct {
			Title string `json:"title"`
		}
		out := captureStdout(t, func() {
			if err := c.run(append(args, "--json")...); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
		})
		if err := json.Unmarshal(out, &rows); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, out)
		}
		var got []string
		for _, r := range rows {
			got = append(got, r.Title)
		}
		return got
	}

	if got := titles("task", "list"); len(got) != 2 {
		t.Fatalf("outside a project, task list = %v, want both", got)
	}
	t.Setenv("ACLINE_PROJECT", "proj-a")
	if got := titles("task", "list"); len(got) != 1 || got[0] != "A task" {
		t.Fatalf("inside proj-a, task list = %v", got)
	}
	if got := titles("spec", "list"); len(got) != 1 || got[0] != "A spec" {
		t.Fatalf("inside proj-a, spec list = %v", got)
	}
	if got := titles("task", "list", "--all-projects"); len(got) != 2 {
		t.Fatalf("task list --all-projects = %v, want both", got)
	}
	if got := titles("task", "list", "--project", "proj-b"); len(got) != 1 || got[0] != "B task" {
		t.Fatalf("task list --project proj-b = %v", got)
	}
	err := c.run("task", "list", "--project", "proj-b", "--all-projects")
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("--project with --all-projects = %v", err)
	}
}

// role list says it shows "global built-ins plus this project's own"; with no
// --project it used to show only the built-ins, even inside the project.
func TestRoleListShowsTheCurrentProjectsRoles(t *testing.T) {
	c := newTestCLI(t)
	if err := c.run("project", "add", "demo", "/tmp/demo"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("role", "add", "--project", "demo", "--kind", "human", "release-manager"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ACLINE_PROJECT", "demo")
	out := captureStdout(t, func() {
		if err := c.run("role", "list"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(string(out), "release-manager") {
		t.Fatalf("role list inside demo misses its own role:\n%s", out)
	}
}

func TestMemoryReviewIsProjectScoped(t *testing.T) {
	c := newTestCLI(t)
	for _, args := range [][]string{
		{"project", "add", "proj-a", "/tmp/proj-a"},
		{"project", "add", "proj-b", "/tmp/proj-b"},
	} {
		if err := c.run(args...); err != nil {
			t.Fatal(err)
		}
	}
	c.st.Actor = store.Actor{Type: "agent", ID: "a"} // an agent's entries wait for review
	for _, args := range [][]string{
		{"memory", "add", "--project", "proj-a", "lesson from a"},
		{"memory", "add", "--project", "proj-b", "lesson from b"},
	} {
		if err := c.run(args...); err != nil {
			t.Fatal(err)
		}
	}
	review := func(args ...string) string {
		t.Helper()
		return string(captureStdout(t, func() {
			if err := c.run(append([]string{"memory", "review"}, args...)...); err != nil {
				t.Fatal(err)
			}
		}))
	}
	if out := review("--project", "proj-a"); !strings.Contains(out, "lesson from a") || strings.Contains(out, "lesson from b") {
		t.Fatalf("review --project proj-a:\n%s", out)
	}
	t.Setenv("ACLINE_PROJECT", "proj-b")
	if out := review(); strings.Contains(out, "lesson from a") || !strings.Contains(out, "lesson from b") {
		t.Fatalf("review inside proj-b:\n%s", out)
	}
	if out := review("--all-projects"); !strings.Contains(out, "lesson from a") || !strings.Contains(out, "lesson from b") {
		t.Fatalf("review --all-projects:\n%s", out)
	}
}

func TestCriteriaUncheckReopensACriterion(t *testing.T) {
	c := newTestCLI(t)
	for _, args := range [][]string{
		{"task", "add", "t"},
		{"task", "criteria", "add", "1", "The system shall work"},
		{"task", "criteria", "check", "1"},
		{"task", "criteria", "uncheck", "1"},
	} {
		if err := c.run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	crit, err := c.st.ListCriteria(1)
	if err != nil || len(crit) != 1 || crit[0].Done {
		t.Fatalf("criteria = %+v, %v; want one, not done", crit, err)
	}
	if err := c.run("task", "criteria", "uncheck", "99"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unchecking a missing criterion = %v", err)
	}
}
