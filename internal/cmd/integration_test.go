package cmd

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"acline/internal/store"
	"acline/internal/worktree"
)

// captureStdout redirects os.Stdout for the duration of fn and returns
// whatever was written — used to assert on --json output, which printJSON
// writes straight to os.Stdout rather than returning it.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stdout
	os.Stdout = w
	fn()
	os.Stdout = prev
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// newTestCLI is a cli around a fresh temp-file store, with the real terminal,
// tree hash and guard (a test replaces them on its own cli).
func newTestCLI(t *testing.T) *cli {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	c := newCLI()
	c.st = s
	return c
}

// run runs one acline command line through a fresh command tree around c,
// with real flag parsing.
func (c *cli) run(args ...string) error {
	root := newRootCmd(c)
	root.SetArgs(args)
	return root.Execute()
}

// TestProjectNoteReflectDecisionFlow exercises the capture -> promote ->
// approve path end to end at the cmd layer: register a project, capture a
// note scoped to it, promote the note to a decision via `reflect promote`,
// and confirm it shows up as a project-scoped decision — the same flow
// verified manually (via the built binary) earlier in development, now as
// a regression test that doesn't depend on a human re-running it by hand.
func TestProjectNoteReflectDecisionFlow(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "demo", "/tmp/demo-repo-does-not-need-to-exist"); err != nil {
		t.Fatalf("project add: %v", err)
	}

	if err := c.run("note", "add", "--project", "demo", "we", "should", "use", "sqlite", "fts5"); err != nil {
		t.Fatalf("note add: %v", err)
	}

	notes, err := st.ListNotes(store.NoteFilter{UnpromotedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("expected 1 unpromoted note, got %d", len(notes))
	}
	noteID := notes[0].ID

	if err := c.run("reflect", "promote", "--decision", "adopt sqlite fts5", "--rationale", "avoids a second index",
		strconv.FormatInt(noteID, 10), "decision", "Use", "FTS5", "for", "search",
	); err != nil {
		t.Fatalf("reflect promote: %v", err)
	}

	n, err := st.GetNote(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if !n.PromotedTo.Valid || n.PromotedTo.String != "decision" {
		t.Fatalf("expected note to be promoted to a decision, got %+v", n)
	}

	p, err := st.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	decisions, err := st.ListDecisions("", &p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Title != "Use FTS5 for search" {
		t.Fatalf("expected 1 project-scoped decision titled %q, got %+v", "Use FTS5 for search", decisions)
	}

	// Re-promoting the same note must be rejected, not silently duplicated.
	if err := c.run("reflect", "promote", strconv.FormatInt(noteID, 10), "decision", "again"); err == nil {
		t.Error("expected promoting an already-promoted note to fail")
	}
}

// TestDecisionAndSpecAddRedactSecrets is a regression test:
// decision/spec free-text fields (context,
// decision, rationale, spec body) previously weren't passed through
// redact.Secrets the way `acline log`/`acline memory add` already were, so
// a pasted live credential in a decision's rationale or a spec's body
// would land in the store unredacted.
func TestDecisionAndSpecAddRedactSecrets(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("decision", "add", "--rationale", "found leaked key AKIAIOSFODNN7EXAMPLE in the old config", "rotate", "creds"); err != nil {
		t.Fatalf("decision add: %v", err)
	}
	decisions, err := st.ListDecisions("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || !strings.Contains(decisions[0].Rationale.String, "[REDACTED]") || strings.Contains(decisions[0].Rationale.String, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("expected decision rationale to be redacted, got %+v", decisions)
	}

	if err := c.run("spec", "add", "--body", "connect via postgres://appuser:hunter2pass@db.internal:5432/prod", "auth", "spec"); err != nil {
		t.Fatalf("spec add: %v", err)
	}
	specs, err := st.ListSpecs("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || !strings.Contains(specs[0].Body.String, "[REDACTED]") || strings.Contains(specs[0].Body.String, "hunter2pass") {
		t.Fatalf("expected spec body to be redacted, got %+v", specs)
	}
	specID := specs[0].ID

	if err := c.run("spec", "revise", "--body", "rotate this key: AKIAIOSFODNN7EXAMPLE", strconv.FormatInt(specID, 10)); err != nil {
		t.Fatalf("spec revise: %v", err)
	}
	revised, err := st.GetSpec(specID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(revised.Body.String, "[REDACTED]") || strings.Contains(revised.Body.String, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("expected revised spec body to be redacted, got %+v", revised)
	}
}

// TestProjectScopedListsIsolateBetweenProjects locks in that data recorded under one project must not leak
// into another project's --project-scoped view, while an unscoped list
// still shows everything.
func TestProjectScopedListsIsolateBetweenProjects(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "proj-a", "/tmp/proj-a"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("project", "add", "proj-b", "/tmp/proj-b"); err != nil {
		t.Fatal(err)
	}

	if err := c.run("task", "add", "--project", "proj-a", "A-only", "task"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("task", "add", "--project", "proj-b", "B-only", "task"); err != nil {
		t.Fatal(err)
	}

	pa, err := st.GetProjectByName("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := st.GetProjectByName("proj-b")
	if err != nil {
		t.Fatal(err)
	}

	aTasks, err := st.ListTasks(store.TaskFilter{ProjectID: &pa.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(aTasks) != 1 || aTasks[0].Title != "A-only task" {
		t.Fatalf("expected proj-a to see only its own task, got %+v", aTasks)
	}

	bTasks, err := st.ListTasks(store.TaskFilter{ProjectID: &pb.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(bTasks) != 1 || bTasks[0].Title != "B-only task" {
		t.Fatalf("expected proj-b to see only its own task, got %+v", bTasks)
	}

	allTasks, err := st.ListTasks(store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(allTasks) != 2 {
		t.Fatalf("expected an unscoped list to show both projects' tasks, got %d", len(allTasks))
	}
}

// TestSearchCmdIsProjectScoped exercises `search` (cmd/search.go), which had
// no test coverage at all before this: a note captured under one project
// must not surface in another project's scoped search, an unscoped search
// must still find it, and the query must actually hit the FTS5 index
// populated at note-add time.
func TestSearchCmdIsProjectScoped(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "proj-a", "/tmp/proj-a"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("project", "add", "proj-b", "/tmp/proj-b"); err != nil {
		t.Fatal(err)
	}

	if err := c.run("note", "add", "--project", "proj-a", "switch", "to", "postgres", "for", "durability"); err != nil {
		t.Fatalf("note add: %v", err)
	}

	searchLimit := 20
	projectID, err := c.resolveProjectFlagOptional("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := st.Search("postgres", projectID, searchLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Kind != "note" {
		t.Fatalf("expected 1 note hit scoped to proj-a, got %+v", hits)
	}

	projectID, err = c.resolveProjectFlagOptional("proj-b")
	if err != nil {
		t.Fatal(err)
	}
	hits, err = st.Search("postgres", projectID, searchLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected proj-b's scoped search to see nothing from proj-a, got %+v", hits)
	}

	hits, err = st.Search("postgres", nil, searchLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected an unscoped search to still find the note, got %+v", hits)
	}
}

// TestJSONOutputOnListCommands covers the --json flag added to search,
// decision list, memory list and note list: each must emit valid JSON that
// round-trips the same data the text path shows, with SQL NULLs coming
// through as JSON null (not database/sql's {"String":"x","Valid":bool}
// shape) — and an empty result set must print `[]`, not the "no X" text
// message, since a script parsing --json output shouldn't have to special-case
// an empty array.
func TestJSONOutputOnListCommands(t *testing.T) {
	c := newTestCLI(t)

	if err := c.run("project", "add", "demo", "/tmp/demo-repo-does-not-need-to-exist"); err != nil {
		t.Fatal(err)
	}

	if err := c.run("decision", "add", "--project", "demo", "--context", "durability", "--decision", "adopt postgres", "use", "postgres"); err != nil {
		t.Fatalf("decision add: %v", err)
	}

	if err := c.run("memory", "add", "--project", "demo", "always", "vacuum", "the", "db"); err != nil {
		t.Fatalf("memory add: %v", err)
	}

	if err := c.run("note", "add", "--project", "demo", "a", "captured", "note"); err != nil {
		t.Fatalf("note add: %v", err)
	}

	if err := c.run("task", "add", "--project", "demo", "write", "docs"); err != nil {
		t.Fatalf("task add: %v", err)
	}

	if err := c.run("spec", "add", "--project", "demo", "auth", "flow"); err != nil {
		t.Fatalf("spec add: %v", err)
	}

	if err := c.run("feature", "add", "--project", "demo", "login"); err != nil {
		t.Fatalf("feature add: %v", err)
	}

	if err := c.run("roadmap", "add", "--project", "demo", "v1", "launch"); err != nil {
		t.Fatalf("roadmap add: %v", err)
	}

	t.Run("decision list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("decision", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []decisionJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Title != "use postgres" {
			t.Fatalf("expected 1 decision titled %q, got %+v", "use postgres", got)
		}
		if got[0].Scope != nil {
			t.Fatalf("expected a NULL scope to decode as nil, got %v", *got[0].Scope)
		}
		if got[0].ProjectID == nil {
			t.Fatal("expected project_id to be set, got nil")
		}
	})

	t.Run("memory list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("memory", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []memoryJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Body != "always vacuum the db" {
			t.Fatalf("expected 1 memory entry, got %+v", got)
		}
	})

	t.Run("note list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("note", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []noteJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Body != "a captured note" {
			t.Fatalf("expected 1 note, got %+v", got)
		}
		if got[0].PromotedTo != nil {
			t.Fatalf("expected an un-promoted note's promoted_to to be nil, got %v", *got[0].PromotedTo)
		}
	})

	t.Run("search --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("search", "--json", "postgres"); err != nil {
				t.Fatal(err)
			}
		})
		var got []searchHitJSON
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Kind != "decision" {
			t.Fatalf("expected 1 decision hit, got %+v", got)
		}
	})

	t.Run("search --json with no results prints an empty array", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("search", "--json", "nonexistentxyz"); err != nil {
				t.Fatal(err)
			}
		})
		var got []searchHitJSON
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("expected an empty JSON array, got %+v", got)
		}
	})

	t.Run("task list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("task", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []taskJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Title != "write docs" {
			t.Fatalf("expected 1 task titled %q, got %+v", "write docs", got)
		}
		if got[0].ProjectID == nil {
			t.Fatal("expected project_id to be set, got nil")
		}
		if got[0].Area != nil {
			t.Fatalf("expected a NULL area to decode as nil, got %v", *got[0].Area)
		}
	})

	t.Run("spec list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("spec", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []specJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Title != "auth flow" {
			t.Fatalf("expected 1 spec titled %q, got %+v", "auth flow", got)
		}
	})

	t.Run("feature list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("feature", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []featureJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Name != "login" {
			t.Fatalf("expected 1 feature named %q, got %+v", "login", got)
		}
	})

	t.Run("roadmap list --json", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := c.run("roadmap", "list", "--json"); err != nil {
				t.Fatal(err)
			}
		})
		var got []milestoneJSONView
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		if len(got) != 1 || got[0].Name != "v1 launch" {
			t.Fatalf("expected 1 milestone named %q, got %+v", "v1 launch", got)
		}
		if got[0].Progress.Total != 0 {
			t.Fatalf("expected a fresh milestone to have 0 total progress, got %+v", got[0].Progress)
		}
	})
}

// TestSessionStartRecordsProjectFromFlag is the fix for the "session <->
// project <-> actor not linked" finding: a session started with --project
// must actually carry that project's id, not just a task link.
func TestSessionStartRecordsProjectFromFlag(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "demo", "/tmp/demo-repo"); err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.run("session", "start", "--project", "demo"); err != nil {
		t.Fatalf("session start: %v", err)
	}

	sess, err := st.CurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ProjectID.Valid || sess.ProjectID.Int64 != p.ID {
		t.Fatalf("expected the session's project_id to be %d, got %+v", p.ID, sess.ProjectID)
	}
}

// TestSessionStartFallsBackToTaskProject confirms the fallback described in
// the finding's own wording ("record which project a session's task
// belongs to, or add a direct project_id"): with no --project and no
// cwd/ACLINE_PROJECT resolution, a session started against a project-scoped
// task inherits that task's project rather than landing unscoped.
func TestSessionStartFallsBackToTaskProject(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "demo", "/tmp/demo-repo"); err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.run("task", "add", "--project", "demo", "scoped", "task"); err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ListTasks(store.TaskFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task under demo, got %+v", tasks)
	}

	if err := c.run("session", "start", "--task", strconv.FormatInt(tasks[0].ID, 10)); err != nil {
		t.Fatalf("session start: %v", err)
	}

	sess, err := st.CurrentSession()
	if err != nil {
		t.Fatal(err)
	}
	if !sess.ProjectID.Valid || sess.ProjectID.Int64 != p.ID {
		t.Fatalf("expected the session to inherit task %d's project (%d), got %+v", tasks[0].ID, p.ID, sess.ProjectID)
	}
}

// TestSessionListFiltersByProject exercises `session list --project`.
func TestSessionListFiltersByProject(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "proj-a", "/tmp/proj-a"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("project", "add", "proj-b", "/tmp/proj-b"); err != nil {
		t.Fatal(err)
	}
	pa, err := st.GetProjectByName("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := st.GetProjectByName("proj-b")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.run("session", "start", "--project", "proj-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndSession("", store.SessionCost{}); err != nil {
		t.Fatal(err)
	}

	if err := c.run("session", "start", "--project", "proj-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndSession("", store.SessionCost{}); err != nil {
		t.Fatal(err)
	}

	aSessions, err := st.ListSessions(&pa.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(aSessions) != 1 {
		t.Fatalf("expected 1 session scoped to proj-a, got %+v", aSessions)
	}

	bSessions, err := st.ListSessions(&pb.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bSessions) != 1 {
		t.Fatalf("expected 1 session scoped to proj-b, got %+v", bSessions)
	}

	allSessions, err := st.ListSessions(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(allSessions) != 2 {
		t.Fatalf("expected an unscoped list to show both sessions, got %d", len(allSessions))
	}
}

// TestSearchTextOutputFlagsNonCurrentStatus is the fix for the "FTS5 index
// staleness on status change" finding: `search`'s plain-text output must
// flag a hit whose source row is no longer in its "current" status (a
// rejected decision), while staying quiet for one that is (an accepted
// decision) — matching the same bracket-annotation convention `memory
// list` already uses for its own status/stale markers.
func TestSearchTextOutputFlagsNonCurrentStatus(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("decision", "add", "--decision", "adopt widgets", "--rationale", "everyone likes widgets", "widgets", "are", "good"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("decision", "add", "--decision", "adopt widgets", "--rationale", "everyone likes widgets", "widgets", "were", "a", "mistake"); err != nil {
		t.Fatal(err)
	}

	decisions, err := st.ListDecisions("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("expected 2 decisions, got %+v", decisions)
	}
	acceptedID := strconv.FormatInt(decisions[0].ID, 10)
	rejectedID := strconv.FormatInt(decisions[1].ID, 10)
	if err := c.run("decision", "accept", acceptedID); err != nil {
		t.Fatal(err)
	}
	if err := c.run("decision", "reject", rejectedID); err != nil {
		t.Fatal(err)
	}

	out := string(captureStdout(t, func() {
		if err := c.run("search", "widgets"); err != nil {
			t.Fatal(err)
		}
	}))

	if !strings.Contains(out, "[decision #"+acceptedID+"] [") {
		t.Errorf("expected the accepted decision's line to carry no status marker, got:\n%s", out)
	}
	if !strings.Contains(out, "[decision #"+rejectedID+"] {rejected} [") {
		t.Errorf("expected the rejected decision's line to carry a {rejected} marker, got:\n%s", out)
	}
}

// TestDepAndEvalCmdsAreProjectScoped is a regression test: dependencies and evals once had no project scoping anywhere
// (schema, store, or cmd layer). Exercises `dep add/list` and `eval record/list` through
// their real RunE, including that dashboard's unverified-deps section
// picks up the same scoping.
func TestDepAndEvalCmdsAreProjectScoped(t *testing.T) {
	c := newTestCLI(t)
	st := c.st

	if err := c.run("project", "add", "proj-a", "/tmp/proj-a"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("project", "add", "proj-b", "/tmp/proj-b"); err != nil {
		t.Fatal(err)
	}
	pa, err := st.GetProjectByName("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := st.GetProjectByName("proj-b")
	if err != nil {
		t.Fatal(err)
	}

	if err := c.run("dep", "add", "--project", "proj-a", "npm", "left-pad"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("dep", "add", "--project", "proj-b", "npm", "right-pad"); err != nil {
		t.Fatal(err)
	}

	if err := c.run("eval", "record", "--project", "proj-a", "--suite", "refactor", "--pass-rate", "0.9"); err != nil {
		t.Fatal(err)
	}
	if err := c.run("eval", "record", "--project", "proj-b", "--suite", "refactor", "--pass-rate", "0.9"); err != nil {
		t.Fatal(err)
	}

	aDeps, err := st.ListDependencies(&pa.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(aDeps) != 1 || aDeps[0].Name != "left-pad" {
		t.Fatalf("expected proj-a to see only its own dependency, got %+v", aDeps)
	}
	bDeps, err := st.ListDependencies(&pb.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(bDeps) != 1 || bDeps[0].Name != "right-pad" {
		t.Fatalf("expected proj-b to see only its own dependency, got %+v", bDeps)
	}
	allDeps, err := st.ListDependencies(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(allDeps) != 2 {
		t.Fatalf("expected an unscoped list to show both projects' dependencies, got %d", len(allDeps))
	}

	aEvals, err := st.ListEvals(&pa.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(aEvals) != 1 {
		t.Fatalf("expected proj-a to see only its own eval, got %+v", aEvals)
	}
	bEvals, err := st.ListEvals(&pb.ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bEvals) != 1 {
		t.Fatalf("expected proj-b to see only its own eval, got %+v", bEvals)
	}

	// dashboard's unverified-deps section must pick up the same scoping.
	out := string(captureStdout(t, func() {
		if err := c.run("dashboard", "--project", "proj-a"); err != nil {
			t.Fatal(err)
		}
	}))
	if !strings.Contains(out, "1 unverified dependency") {
		t.Errorf("expected dashboard --project proj-a to report exactly 1 unverified dependency, got:\n%s", out)
	}
}

// TestLogRequiresType: a bare `acline log "..."` used to
// default to a note-type history event, easy to mistake for `acline note
// add`, which is what actually feeds /reflect.
func TestLogRequiresType(t *testing.T) {
	c := newTestCLI(t)
	st := c.st
	err := c.run("log", "fixed", "the", "bug")
	if err == nil || !strings.Contains(err.Error(), "acline note add") {
		t.Fatalf("expected a missing --type to be refused with a pointer to `acline note add`, got %v", err)
	}

	// Internal event types are written only by the command that performs
	// the action; a direct log must not be able to forge one.
	for _, internal := range []string{"approval", "override", "guard_denied", "check"} {
		if err := c.run("log", "--type", internal, "forged"); err == nil {
			t.Errorf("expected --type %s to be refused", internal)
		}
	}

	captureStdout(t, func() {
		if err := c.run("log", "--type", "bug", "fixed", "the", "bug"); err != nil {
			t.Fatalf("log --type bug: %v", err)
		}
	})
	events, err := st.ListEvents(nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Type != "bug" {
		t.Fatalf("expected a bug event, got %+v", events)
	}
}

// TestTaskAddWithRoleAndUnknownRoleIsRefused is the cmd-layer regression
// test for the roles feature (acline spec #2): `task add --role` resolves
// a real role to the task's role_id, an unknown role name is refused with
// a clear error rather than silently ignored, and omitting --role entirely
// still creates a task with role_id unset -- exactly today's behavior.
func TestTaskAddWithRoleAndUnknownRoleIsRefused(t *testing.T) {
	c := newTestCLI(t)
	s := c.st

	if err := c.run("task", "add", "--role", "qa", "verify", "the", "build"); err != nil {
		t.Fatalf("task add --role qa: %v", err)
	}
	tasks, err := s.ListTasks(store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || !tasks[0].RoleID.Valid {
		t.Fatalf("expected the task's role_id to be set from --role qa, got %+v", tasks)
	}
	qaRole, err := s.GetRoleByName(nil, "qa")
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].RoleID.Int64 != qaRole.ID {
		t.Fatalf("expected role_id %d (qa), got %d", qaRole.ID, tasks[0].RoleID.Int64)
	}

	if err := c.run("task", "add", "--role", "not-a-real-role", "another", "task"); err == nil {
		t.Fatal("expected an unknown --role to be refused")
	}

	if err := c.run("task", "add", "unroled", "task"); err != nil {
		t.Fatalf("task add with no --role: %v", err)
	}
	tasks, err = s.ListTasks(store.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected exactly 2 tasks (the refused one must not have been created), got %d", len(tasks))
	}
}

// TestRoleAddAndList exercises `acline role add`/`role list` at the cmd
// layer: a project-scoped role is created, visible alongside the 7 global
// seeds when scoped to that project, and invisible to a different project.
func TestRoleAddAndList(t *testing.T) {
	c := newTestCLI(t)
	s := c.st

	if _, err := s.AddProject("demo", "/tmp/demo-role-cmd", "hotl"); err != nil {
		t.Fatal(err)
	}

	if err := c.run("role", "add", "--project", "demo", "--kind", "human", "--can-approve", "--description", "release manager", "release-manager"); err != nil {
		t.Fatalf("role add: %v", err)
	}

	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	roles, err := s.ListRoles(&p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 8 { // 7 global + this project's own
		t.Fatalf("expected 8 roles visible to project demo, got %d: %+v", len(roles), roles)
	}

	unscoped, err := s.ListRoles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(unscoped) != 7 {
		t.Fatalf("expected release-manager to stay invisible with no project scope, got %d roles", len(unscoped))
	}
}

// TestTaskAssignLogsHandoffEvent exercises `acline task assign` at the cmd
// layer: it sets the task's role, logs a role_assigned event with the
// old->new names, and reassigning again logs the next hop correctly.
func TestTaskAssignLogsHandoffEvent(t *testing.T) {
	c := newTestCLI(t)
	s := c.st

	taskID, err := s.AddTask("assign me", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if err := c.run("task", "assign", strconv.FormatInt(taskID, 10), "designer"); err != nil {
		t.Fatalf("task assign designer: %v", err)
	}
	task, err := s.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	designer, err := s.GetRoleByName(nil, "designer")
	if err != nil {
		t.Fatal(err)
	}
	if !task.RoleID.Valid || task.RoleID.Int64 != designer.ID {
		t.Fatalf("expected role_id = designer, got %+v", task.RoleID)
	}

	if err := c.run("task", "assign", strconv.FormatInt(taskID, 10), "developer"); err != nil {
		t.Fatalf("task assign developer: %v", err)
	}
	events, err := s.ListEvents(&taskID, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, e := range events {
		if e.Type == "role_assigned" {
			found = append(found, e.Message)
		}
	}
	// ListEvents is newest-first: found[0] is the second assignment,
	// found[1] the first.
	want := []string{"designer -> developer", "unassigned -> designer"}
	if len(found) != len(want) || found[0] != want[0] || found[1] != want[1] {
		t.Fatalf("expected role_assigned messages %v, got %v", want, found)
	}

	if err := c.run("task", "assign", strconv.FormatInt(taskID, 10), "not-a-role"); err == nil {
		t.Fatal("expected an unknown role name to be refused")
	}
}

// `project use` writes a marker, and a marker outside the project's registered
// path is ignored, so refusing is better than writing a file that does nothing.
func TestProjectUseRefusesADirectoryOutsideTheProject(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	root := t.TempDir()
	if _, err := s.AddProject("demo", root, "hotl"); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	t.Chdir(other)
	if err := c.run("project", "use", "demo"); err == nil {
		t.Fatal("wrote a marker that would be ignored")
	}
	if _, err := os.Stat(filepath.Join(other, ".acline-project")); err == nil {
		t.Error("marker file was written anyway")
	}
	sub := filepath.Join(root, "svc")
	os.MkdirAll(sub, 0o755)
	t.Chdir(sub)
	captureStdout(t, func() {
		if err := c.run("project", "use", "demo"); err != nil {
			t.Fatalf("inside the project: %v", err)
		}
	})
}

// A passing `check run` is about the code as it was. Once the code changes, a
// high-risk task must not complete on it.
func TestCheckRunBindsAPassToTheTreeAndTheGateNoticesWhenItChanges(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	id, err := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		if err := c.run("check", "run", "--kind", "test", "--cmd", "true", strconv.FormatInt(id, 10)); err != nil {
			t.Fatal(err)
		}
	})
	checks, _ := s.ListChecks(id)
	if len(checks) != 1 || checks[0].Source != store.CheckSourceRunner || checks[0].Status != "pass" {
		t.Fatalf("check = %+v", checks)
	}
	if want := c.currentTree(); want == "" || checks[0].TreeHash.String != want {
		t.Fatalf("recorded tree %q, working tree is %q", checks[0].TreeHash.String, want)
	}
	if _, err := s.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}

	// unchanged code: the gate is satisfied
	out := captureStdout(t, func() { c.run("task", "gate", strconv.FormatInt(id, 10)) })
	if !strings.Contains(string(out), "gate satisfied") {
		t.Fatalf("gate output: %s", out)
	}

	// the code changes: the same pass no longer counts, and `task done` refuses
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// changed after the check ran\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { c.run("task", "gate", strconv.FormatInt(id, 10)) })
	if !strings.Contains(string(out), "NOT satisfied") || !strings.Contains(string(out), "code has changed") {
		t.Fatalf("stale evidence went unnoticed: %s", out)
	}
	if err := c.run("task", "done", strconv.FormatInt(id, 10)); err == nil {
		t.Fatal("task done completed on evidence about older code")
	}

	// re-running against the new code fixes it
	captureStdout(t, func() { c.run("check", "run", "--kind", "test", "--cmd", "true", strconv.FormatInt(id, 10)) })
	if err := c.run("task", "done", strconv.FormatInt(id, 10)); err != nil {
		t.Fatalf("after re-running: %v", err)
	}
}

// `check run --cmd true` used to record a runner pass for any agent, which made
// "high risk needs a check acline ran" meaningless. The command must not even run.
func TestAgentCheckRunWithCmdIsRefusedBeforeItRuns(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	s.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	dir := t.TempDir()
	t.Chdir(dir)
	id, _ := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high"})

	marker := filepath.Join(dir, "ran")
	err := c.run("check", "run", "--kind", "test", "--cmd", "touch "+marker, strconv.FormatInt(id, 10))
	if !errors.Is(err, store.ErrAgentCannotChooseCheckCommand) {
		t.Fatalf("agent check run --cmd = %v, want ErrAgentCannotChooseCheckCommand", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the refused command ran anyway")
	}
	if checks, _ := s.ListChecks(id); len(checks) != 0 {
		t.Fatalf("a refused check was recorded: %+v", checks)
	}
}

func TestCheckRecordIsManualAndRecordsTheTree(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	t.Chdir(dir)
	id, _ := s.AddTask("t", "", "normal", store.TaskOpts{})
	captureStdout(t, func() {
		if err := c.run("check", "record", "--kind", "lint", "--status", "pass", "--detail", "by hand", strconv.FormatInt(id, 10)); err != nil {
			t.Fatal(err)
		}
	})
	checks, _ := s.ListChecks(id)
	if len(checks) != 1 || checks[0].Source != store.CheckSourceManual || checks[0].TreeHash.String != c.currentTree() {
		t.Fatalf("check = %+v", checks)
	}
}

// A tree that cannot be fingerprinted used to read as "nothing changed".
func TestHighRiskGateBlocksWhenTheTreeCannotBeFingerprinted(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	dir := t.TempDir()
	t.Chdir(dir)
	id, _ := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high"})
	if _, err := s.AddCheckWithMeta(id, nil, "test", "pass", "ran: go test", "",
		store.CheckMeta{Source: store.CheckSourceRunner, TreeHash: "sha256:old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}
	c.hashTree = func(string) string { return "" }

	out := captureStdout(t, func() { c.run("task", "gate", strconv.FormatInt(id, 10)) })
	if !strings.Contains(string(out), "NOT satisfied") || !strings.Contains(string(out), "could not be fingerprinted") {
		t.Fatalf("gate output: %s", out)
	}
	if err := c.run("task", "done", strconv.FormatInt(id, 10)); err == nil {
		t.Fatal("task done completed although the tree could not be checked")
	}
}

// `check run` and `task done` used the current directory for both the run and
// the tree fingerprint, so from a subfolder only that subfolder's tests ran and
// only its files were fingerprinted: a high-risk task completed while tests
// elsewhere failed, and a change outside the subfolder went unnoticed. They now
// use the task's registered project path, as the MCP tools do.
func TestCheckRunAndGateUseTheProjectRootFromASubfolder(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	root := t.TempDir()
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "x.go"), []byte("package "+d+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pid, err := s.AddProject("demo", root, "")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high", ProjectID: &pid})
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "b"))

	captureStdout(t, func() {
		if err := c.run("check", "run", "--kind", "test", "--cmd", "pwd", strconv.FormatInt(id, 10)); err != nil {
			t.Fatal(err)
		}
	})
	checks, _ := s.ListChecks(id)
	if len(checks) != 1 || checks[0].Status != "pass" {
		t.Fatalf("check = %+v", checks)
	}
	lines := strings.Split(strings.TrimSpace(checks[0].Detail.String), "\n")
	if ranIn := store.RealPath(lines[len(lines)-1]); ranIn != store.RealPath(root) {
		t.Errorf("the check ran in %s, want the project root %s", ranIn, store.RealPath(root))
	}
	if rootTree := worktree.Hash(root); checks[0].TreeHash.String != rootTree {
		t.Fatalf("recorded tree %q, the project root's is %q (the subfolder's is %q)",
			checks[0].TreeHash.String, rootTree, worktree.Hash(filepath.Join(root, "b")))
	}
	if _, err := s.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}

	// A change outside the subfolder makes the pass stale, seen from the subfolder too.
	if err := os.WriteFile(filepath.Join(root, "a", "x.go"), []byte("package a\n\nvar changed = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = c.run("task", "done", strconv.FormatInt(id, 10))
	if err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("task done from a subfolder after a change elsewhere = %v, want the stale-check blocker", err)
	}
}

// `acline approve` records the code it approved, so the gate can ask for a new
// approval after the code changes.
func TestApproveRecordsTheTreeAndTheGateWantsANewOneAfterAChange(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, err := s.AddProject("demo", root, "")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high", ProjectID: &pid})
	t.Chdir(root)
	idArg := strconv.FormatInt(id, 10)
	captureStdout(t, func() {
		if err := c.run("approve", idArg); err != nil {
			t.Fatal(err)
		}
	})
	approvals, _ := s.ListApprovals(id)
	if len(approvals) != 1 || approvals[0].TreeHash.String != worktree.Hash(root) {
		t.Fatalf("approval = %+v, want the project's tree %q", approvals, worktree.Hash(root))
	}
	if err := os.WriteFile(filepath.Join(root, "x.go"), []byte("package x\n\nvar y = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := s.EvaluateGateForTree(id, c.taskGateTree(id))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(g.Blockers, "\n"), "different version of the code") {
		t.Fatalf("after a change the old approval still counted: %+v", g)
	}
}

// `task add` sent its flag defaults (risk low, autonomy hotl) as if typed, so
// a project's defaults never applied. Only typed flags are sent now.
func TestTaskAddUsesTheProjectsDefaultsUnlessFlagsAreTyped(t *testing.T) {
	c := newTestCLI(t)
	s := c.st
	pid, err := s.AddProjectWithDefaults("strict", "", "hitl", "medium", "")
	if err != nil {
		t.Fatal(err)
	}
	add := func(title string, flags ...string) *store.Task {
		captureStdout(t, func() {
			if err := c.run(append(append([]string{"task", "add", "--project", "strict"}, flags...), title)...); err != nil {
				t.Fatal(err)
			}
		})
		tasks, _ := s.ListTasks(store.TaskFilter{ProjectID: &pid})
		return &tasks[len(tasks)-1]
	}
	if got := add("defaults"); got.Risk != "medium" || got.Autonomy != "hitl" {
		t.Fatalf("no flags: %s/%s, want the project's medium/hitl", got.Risk, got.Autonomy)
	}
	if got := add("typed", "--risk", "low", "--autonomy", "hotl"); got.Risk != "low" || got.Autonomy != "hotl" {
		t.Fatalf("typed flags: %s/%s, want low/hotl", got.Risk, got.Autonomy)
	}
}
