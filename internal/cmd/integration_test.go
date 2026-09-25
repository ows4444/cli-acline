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
	"time"

	"acline/internal/store"
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

// withTestStore points the package-level `st` (normally opened by
// rootCmd's PersistentPreRunE from --db/$ACLINE_DB) at a fresh temp-file
// store for the duration of the test, and restores whatever was there
// before. Commands under test are invoked by calling their RunE directly
// with package-level flag vars set, rather than through cobra's argument
// parser — cheap and sufficient for exercising the command logic itself.
func withTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	prev := st
	st = s
	t.Cleanup(func() { st = prev })
	return s
}

// TestProjectNoteReflectDecisionFlow exercises the capture -> promote ->
// approve path end to end at the cmd layer: register a project, capture a
// note scoped to it, promote the note to a decision via `reflect promote`,
// and confirm it shows up as a project-scoped decision — the same flow
// verified manually (via the built binary) earlier in development, now as
// a regression test that doesn't depend on a human re-running it by hand.
func TestProjectNoteReflectDecisionFlow(t *testing.T) {
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"demo", "/tmp/demo-repo-does-not-need-to-exist"}); err != nil {
		t.Fatalf("project add: %v", err)
	}

	noteProject = "demo"
	noteSource = "manual"
	t.Cleanup(func() { noteProject, noteSource = "", "manual" })
	if err := noteAddCmd.RunE(noteAddCmd, []string{"we", "should", "use", "sqlite", "fts5"}); err != nil {
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

	reflectDecisionText = "adopt sqlite fts5"
	reflectDecisionRationale = "avoids a second index"
	t.Cleanup(func() { reflectDecisionText, reflectDecisionRationale = "", "" })
	if err := reflectPromoteCmd.RunE(reflectPromoteCmd, []string{
		strconv.FormatInt(noteID, 10), "decision", "Use", "FTS5", "for", "search",
	}); err != nil {
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
	if err := reflectPromoteCmd.RunE(reflectPromoteCmd, []string{strconv.FormatInt(noteID, 10), "decision", "again"}); err == nil {
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
	withTestStore(t)

	decisionRationale = "found leaked key AKIAIOSFODNN7EXAMPLE in the old config"
	t.Cleanup(func() {
		decisionScope, decisionContext, decisionText, decisionRationale, decisionProject = "", "", "", "", ""
	})
	if err := decisionAddCmd.RunE(decisionAddCmd, []string{"rotate", "creds"}); err != nil {
		t.Fatalf("decision add: %v", err)
	}
	decisions, err := st.ListDecisions("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || !strings.Contains(decisions[0].Rationale.String, "[REDACTED]") || strings.Contains(decisions[0].Rationale.String, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("expected decision rationale to be redacted, got %+v", decisions)
	}

	specBody = "connect via postgres://appuser:hunter2pass@db.internal:5432/prod"
	t.Cleanup(func() { specBody, specBodyFile, specProject = "", "", "" })
	if err := specAddCmd.RunE(specAddCmd, []string{"auth", "spec"}); err != nil {
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

	specBody = "rotate this key: AKIAIOSFODNN7EXAMPLE"
	if err := specReviseCmd.RunE(specReviseCmd, []string{strconv.FormatInt(specID, 10)}); err != nil {
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-a", "/tmp/proj-a"}); err != nil {
		t.Fatal(err)
	}
	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-b", "/tmp/proj-b"}); err != nil {
		t.Fatal(err)
	}

	taskProject = "proj-a"
	t.Cleanup(func() { taskProject = "" })
	if err := taskAddCmd.RunE(taskAddCmd, []string{"A-only", "task"}); err != nil {
		t.Fatal(err)
	}
	taskProject = "proj-b"
	if err := taskAddCmd.RunE(taskAddCmd, []string{"B-only", "task"}); err != nil {
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-a", "/tmp/proj-a"}); err != nil {
		t.Fatal(err)
	}
	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-b", "/tmp/proj-b"}); err != nil {
		t.Fatal(err)
	}

	noteProject = "proj-a"
	noteSource = "manual"
	t.Cleanup(func() { noteProject, noteSource = "", "manual" })
	if err := noteAddCmd.RunE(noteAddCmd, []string{"switch", "to", "postgres", "for", "durability"}); err != nil {
		t.Fatalf("note add: %v", err)
	}

	searchLimit = 20
	t.Cleanup(func() { searchProject, searchLimit = "", 20 })

	searchProject = "proj-a"
	projectID, err := resolveProjectFlagOptional(searchProject)
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

	searchProject = "proj-b"
	projectID, err = resolveProjectFlagOptional(searchProject)
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"demo", "/tmp/demo-repo-does-not-need-to-exist"}); err != nil {
		t.Fatal(err)
	}

	decisionProject = "demo"
	decisionContext = "durability"
	decisionText = "adopt postgres"
	t.Cleanup(func() { decisionProject, decisionContext, decisionText = "", "", "" })
	if err := decisionAddCmd.RunE(decisionAddCmd, []string{"use", "postgres"}); err != nil {
		t.Fatalf("decision add: %v", err)
	}

	memoryProject = "demo"
	t.Cleanup(func() { memoryProject = "" })
	if err := memoryAddCmd.RunE(memoryAddCmd, []string{"always", "vacuum", "the", "db"}); err != nil {
		t.Fatalf("memory add: %v", err)
	}

	noteProject = "demo"
	t.Cleanup(func() { noteProject = "" })
	if err := noteAddCmd.RunE(noteAddCmd, []string{"a", "captured", "note"}); err != nil {
		t.Fatalf("note add: %v", err)
	}

	taskProject = "demo"
	t.Cleanup(func() { taskProject = "" })
	if err := taskAddCmd.RunE(taskAddCmd, []string{"write", "docs"}); err != nil {
		t.Fatalf("task add: %v", err)
	}

	specProject = "demo"
	t.Cleanup(func() { specProject = "" })
	if err := specAddCmd.RunE(specAddCmd, []string{"auth", "flow"}); err != nil {
		t.Fatalf("spec add: %v", err)
	}

	featureProject = "demo"
	t.Cleanup(func() { featureProject = "" })
	if err := featureAddCmd.RunE(featureAddCmd, []string{"login"}); err != nil {
		t.Fatalf("feature add: %v", err)
	}

	roadmapProject = "demo"
	t.Cleanup(func() { roadmapProject = "" })
	if err := roadmapAddCmd.RunE(roadmapAddCmd, []string{"v1", "launch"}); err != nil {
		t.Fatalf("roadmap add: %v", err)
	}

	t.Run("decision list --json", func(t *testing.T) {
		decisionJSON = true
		t.Cleanup(func() { decisionJSON = false })
		out := captureStdout(t, func() {
			if err := decisionListCmd.RunE(decisionListCmd, nil); err != nil {
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
		memoryJSON = true
		t.Cleanup(func() { memoryJSON = false })
		out := captureStdout(t, func() {
			if err := memoryListCmd.RunE(memoryListCmd, nil); err != nil {
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
		noteJSON = true
		t.Cleanup(func() { noteJSON = false })
		out := captureStdout(t, func() {
			if err := noteListCmd.RunE(noteListCmd, nil); err != nil {
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
		searchJSON = true
		searchLimit = 20
		t.Cleanup(func() { searchJSON, searchLimit = false, 20 })
		out := captureStdout(t, func() {
			if err := searchCmd.RunE(searchCmd, []string{"postgres"}); err != nil {
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
		searchJSON = true
		searchLimit = 20
		t.Cleanup(func() { searchJSON, searchLimit = false, 20 })
		out := captureStdout(t, func() {
			if err := searchCmd.RunE(searchCmd, []string{"nonexistentxyz"}); err != nil {
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
		listJSON = true
		t.Cleanup(func() { listJSON = false })
		out := captureStdout(t, func() {
			if err := taskListCmd.RunE(taskListCmd, nil); err != nil {
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
		specListJSON = true
		t.Cleanup(func() { specListJSON = false })
		out := captureStdout(t, func() {
			if err := specListCmd.RunE(specListCmd, nil); err != nil {
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
		featureListJSON = true
		t.Cleanup(func() { featureListJSON = false })
		out := captureStdout(t, func() {
			if err := featureListCmd.RunE(featureListCmd, nil); err != nil {
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
		roadmapListJSON = true
		t.Cleanup(func() { roadmapListJSON = false })
		out := captureStdout(t, func() {
			if err := roadmapListCmd.RunE(roadmapListCmd, nil); err != nil {
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"demo", "/tmp/demo-repo"}); err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}

	sessionStartProject = "demo"
	t.Cleanup(func() { sessionStartProject = "" })
	if err := sessionStartCmd.RunE(sessionStartCmd, nil); err != nil {
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"demo", "/tmp/demo-repo"}); err != nil {
		t.Fatal(err)
	}
	p, err := st.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}

	taskProject = "demo"
	t.Cleanup(func() { taskProject = "" })
	if err := taskAddCmd.RunE(taskAddCmd, []string{"scoped", "task"}); err != nil {
		t.Fatal(err)
	}
	tasks, err := st.ListTasks(store.TaskFilter{ProjectID: &p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task under demo, got %+v", tasks)
	}

	sessionStartTask = strconv.FormatInt(tasks[0].ID, 10)
	t.Cleanup(func() { sessionStartTask = "" })
	if err := sessionStartCmd.RunE(sessionStartCmd, nil); err != nil {
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-a", "/tmp/proj-a"}); err != nil {
		t.Fatal(err)
	}
	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-b", "/tmp/proj-b"}); err != nil {
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

	sessionStartProject = "proj-a"
	t.Cleanup(func() { sessionStartProject = "" })
	if err := sessionStartCmd.RunE(sessionStartCmd, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EndSession("", store.SessionCost{}); err != nil {
		t.Fatal(err)
	}

	sessionStartProject = "proj-b"
	if err := sessionStartCmd.RunE(sessionStartCmd, nil); err != nil {
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
	withTestStore(t)

	decisionText, decisionRationale = "adopt widgets", "everyone likes widgets"
	t.Cleanup(func() { decisionText, decisionRationale = "", "" })
	if err := decisionAddCmd.RunE(decisionAddCmd, []string{"widgets", "are", "good"}); err != nil {
		t.Fatal(err)
	}
	if err := decisionAddCmd.RunE(decisionAddCmd, []string{"widgets", "were", "a", "mistake"}); err != nil {
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
	if err := decisionAcceptCmd.RunE(decisionAcceptCmd, []string{acceptedID}); err != nil {
		t.Fatal(err)
	}
	if err := decisionRejectCmd.RunE(decisionRejectCmd, []string{rejectedID}); err != nil {
		t.Fatal(err)
	}

	searchLimit = 20
	t.Cleanup(func() { searchLimit = 20 })
	out := string(captureStdout(t, func() {
		if err := searchCmd.RunE(searchCmd, []string{"widgets"}); err != nil {
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
	withTestStore(t)

	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-a", "/tmp/proj-a"}); err != nil {
		t.Fatal(err)
	}
	if err := projectAddCmd.RunE(projectAddCmd, []string{"proj-b", "/tmp/proj-b"}); err != nil {
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

	depProject = "proj-a"
	t.Cleanup(func() { depProject = "" })
	if err := depAddCmd.RunE(depAddCmd, []string{"npm", "left-pad"}); err != nil {
		t.Fatal(err)
	}
	depProject = "proj-b"
	if err := depAddCmd.RunE(depAddCmd, []string{"npm", "right-pad"}); err != nil {
		t.Fatal(err)
	}

	evalProject = "proj-a"
	evalSuite, evalPassRate = "refactor", 0.9
	t.Cleanup(func() { evalProject, evalSuite, evalPassRate = "", "", 0 })
	if err := evalRecordCmd.RunE(evalRecordCmd, nil); err != nil {
		t.Fatal(err)
	}
	evalProject = "proj-b"
	if err := evalRecordCmd.RunE(evalRecordCmd, nil); err != nil {
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
	dashboardProject = "proj-a"
	t.Cleanup(func() { dashboardProject = "" })
	out := string(captureStdout(t, func() {
		if err := dashboardCmd.RunE(dashboardCmd, nil); err != nil {
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
	withTestStore(t)
	t.Cleanup(func() { logType, logTask = "", "" })

	logType = ""
	err := logCmd.RunE(logCmd, []string{"fixed", "the", "bug"})
	if err == nil || !strings.Contains(err.Error(), "acline note add") {
		t.Fatalf("expected a missing --type to be refused with a pointer to `acline note add`, got %v", err)
	}

	// Internal event types are written only by the command that performs
	// the action; a direct log must not be able to forge one.
	for _, internal := range []string{"approval", "override", "guard_denied", "check"} {
		logType = internal
		if err := logCmd.RunE(logCmd, []string{"forged"}); err == nil {
			t.Errorf("expected --type %s to be refused", internal)
		}
	}

	logType = "bug"
	captureStdout(t, func() {
		if err := logCmd.RunE(logCmd, []string{"fixed", "the", "bug"}); err != nil {
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
	s := withTestStore(t)
	t.Cleanup(func() {
		taskArea, taskType, taskRisk, taskAutonomy, taskSpec, taskMilestone, taskProject, taskRole = "", "", "", "", "", "", "", ""
	})

	taskRole = "qa"
	if err := taskAddCmd.RunE(taskAddCmd, []string{"verify", "the", "build"}); err != nil {
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

	taskRole = "not-a-real-role"
	if err := taskAddCmd.RunE(taskAddCmd, []string{"another", "task"}); err == nil {
		t.Fatal("expected an unknown --role to be refused")
	}

	taskRole = ""
	if err := taskAddCmd.RunE(taskAddCmd, []string{"unroled", "task"}); err != nil {
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
	s := withTestStore(t)
	t.Cleanup(func() { roleAddKind, roleAddCanApprove, roleAddDescription, roleProject = "both", false, "", "" })

	if _, err := s.AddProject("demo", "/tmp/demo-role-cmd", "hotl"); err != nil {
		t.Fatal(err)
	}

	roleProject = "demo"
	roleAddKind = "human"
	roleAddCanApprove = true
	roleAddDescription = "release manager"
	if err := roleAddCmd.RunE(roleAddCmd, []string{"release-manager"}); err != nil {
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
	s := withTestStore(t)
	t.Cleanup(func() { taskAssignProject = "" })

	taskID, err := s.AddTask("assign me", "", "normal", store.TaskOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if err := taskAssignCmd.RunE(taskAssignCmd, []string{strconv.FormatInt(taskID, 10), "designer"}); err != nil {
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

	if err := taskAssignCmd.RunE(taskAssignCmd, []string{strconv.FormatInt(taskID, 10), "developer"}); err != nil {
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

	if err := taskAssignCmd.RunE(taskAssignCmd, []string{strconv.FormatInt(taskID, 10), "not-a-role"}); err == nil {
		t.Fatal("expected an unknown role name to be refused")
	}
}

// `project use` writes a marker, and a marker outside the project's registered
// path is ignored, so refusing is better than writing a file that does nothing.
func TestProjectUseRefusesADirectoryOutsideTheProject(t *testing.T) {
	s := withTestStore(t)
	root := t.TempDir()
	if _, err := s.AddProject("demo", root, "hotl"); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	t.Chdir(other)
	if err := projectUseCmd.RunE(projectUseCmd, []string{"demo"}); err == nil {
		t.Fatal("wrote a marker that would be ignored")
	}
	if _, err := os.Stat(filepath.Join(other, ".acline-project")); err == nil {
		t.Error("marker file was written anyway")
	}
	sub := filepath.Join(root, "svc")
	os.MkdirAll(sub, 0o755)
	t.Chdir(sub)
	captureStdout(t, func() {
		if err := projectUseCmd.RunE(projectUseCmd, []string{"demo"}); err != nil {
			t.Fatalf("inside the project: %v", err)
		}
	})
}

// A passing `check run` is about the code as it was. Once the code changes, a
// high-risk task must not complete on it.
func TestCheckRunBindsAPassToTheTreeAndTheGateNoticesWhenItChanges(t *testing.T) {
	s := withTestStore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	id, err := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high"})
	if err != nil {
		t.Fatal(err)
	}

	checkRunKind, checkRunCommand, checkRunTimeout = "test", "true", time.Minute
	t.Cleanup(func() { checkRunKind, checkRunCommand, checkRunTimeout = "", "", 0 })
	captureStdout(t, func() {
		if err := checkRunCmd.RunE(checkRunCmd, []string{strconv.FormatInt(id, 10)}); err != nil {
			t.Fatal(err)
		}
	})
	checks, _ := s.ListChecks(id)
	if len(checks) != 1 || checks[0].Source != store.CheckSourceRunner || checks[0].Status != "pass" {
		t.Fatalf("check = %+v", checks)
	}
	if want := currentTree(); want == "" || checks[0].TreeHash.String != want {
		t.Fatalf("recorded tree %q, working tree is %q", checks[0].TreeHash.String, want)
	}
	if _, err := s.AddApproval(id, "code_review", "bob", "approved", ""); err != nil {
		t.Fatal(err)
	}

	// unchanged code: the gate is satisfied
	out := captureStdout(t, func() { taskGateCmd.RunE(taskGateCmd, []string{strconv.FormatInt(id, 10)}) })
	if !strings.Contains(string(out), "gate satisfied") {
		t.Fatalf("gate output: %s", out)
	}

	// the code changes: the same pass no longer counts, and `task done` refuses
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n// changed after the check ran\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { taskGateCmd.RunE(taskGateCmd, []string{strconv.FormatInt(id, 10)}) })
	if !strings.Contains(string(out), "NOT satisfied") || !strings.Contains(string(out), "code has changed") {
		t.Fatalf("stale evidence went unnoticed: %s", out)
	}
	if err := taskDoneCmd.RunE(taskDoneCmd, []string{strconv.FormatInt(id, 10)}); err == nil {
		t.Fatal("task done completed on evidence about older code")
	}

	// re-running against the new code fixes it
	captureStdout(t, func() { checkRunCmd.RunE(checkRunCmd, []string{strconv.FormatInt(id, 10)}) })
	if err := taskDoneCmd.RunE(taskDoneCmd, []string{strconv.FormatInt(id, 10)}); err != nil {
		t.Fatalf("after re-running: %v", err)
	}
}

// `check run --cmd true` used to record a runner pass for any agent, which made
// "high risk needs a check acline ran" meaningless. The command must not even run.
func TestAgentCheckRunWithCmdIsRefusedBeforeItRuns(t *testing.T) {
	s := withTestStore(t)
	s.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	dir := t.TempDir()
	t.Chdir(dir)
	id, _ := s.AddTask("risky", "", "normal", store.TaskOpts{Risk: "high"})

	marker := filepath.Join(dir, "ran")
	checkRunKind, checkRunCommand, checkRunTimeout = "test", "touch "+marker, time.Minute
	t.Cleanup(func() { checkRunKind, checkRunCommand, checkRunTimeout = "", "", 0 })
	err := checkRunCmd.RunE(checkRunCmd, []string{strconv.FormatInt(id, 10)})
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
	s := withTestStore(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	t.Chdir(dir)
	id, _ := s.AddTask("t", "", "normal", store.TaskOpts{})
	checkKind, checkStatus, checkDetail = "lint", "pass", "by hand"
	t.Cleanup(func() { checkKind, checkStatus, checkDetail = "", "", "" })
	captureStdout(t, func() {
		if err := checkRecordCmd.RunE(checkRecordCmd, []string{strconv.FormatInt(id, 10)}); err != nil {
			t.Fatal(err)
		}
	})
	checks, _ := s.ListChecks(id)
	if len(checks) != 1 || checks[0].Source != store.CheckSourceManual || checks[0].TreeHash.String != currentTree() {
		t.Fatalf("check = %+v", checks)
	}
}

// A tree that cannot be fingerprinted used to read as "nothing changed".
func TestHighRiskGateBlocksWhenTheTreeCannotBeFingerprinted(t *testing.T) {
	s := withTestStore(t)
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
	prev := hashTree
	hashTree = func(string) string { return "" }
	t.Cleanup(func() { hashTree = prev })

	out := captureStdout(t, func() { taskGateCmd.RunE(taskGateCmd, []string{strconv.FormatInt(id, 10)}) })
	if !strings.Contains(string(out), "NOT satisfied") || !strings.Contains(string(out), "could not be fingerprinted") {
		t.Fatalf("gate output: %s", out)
	}
	if err := taskDoneCmd.RunE(taskDoneCmd, []string{strconv.FormatInt(id, 10)}); err == nil {
		t.Fatal("task done completed although the tree could not be checked")
	}
}
