package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProjectAddListAndResolveByName(t *testing.T) {
	s := humanStore(t)
	id, err := s.AddProject("demo", "/tmp/does-not-need-to-exist", "hotl")
	if err != nil {
		t.Fatal(err)
	}
	projects, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].ID != id || projects[0].Name != "demo" {
		t.Fatalf("unexpected project list: %+v", projects)
	}

	p, err := s.GetProjectByName("demo")
	if err != nil {
		t.Fatal(err)
	}
	if p.AutonomyDefault != "hotl" {
		t.Errorf("autonomy_default = %q, want hotl", p.AutonomyDefault)
	}

	if _, err := s.GetProjectByName("missing"); err == nil {
		t.Error("expected error resolving an unregistered project")
	}
}

func TestNoteCaptureAndPromotion(t *testing.T) {
	s := agentStore(t)
	// No path: registering one is a person's action (see project_auth_test.go).
	projID, err := s.AddProject("demo", "", "hotl")
	if err != nil {
		t.Fatal(err)
	}

	id, err := s.AddNote(&projID, "we should cache this", "conversation")
	if err != nil {
		t.Fatal(err)
	}

	unpromoted, err := s.ListNotes(NoteFilter{ProjectID: &projID, UnpromotedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(unpromoted) != 1 || unpromoted[0].ID != id {
		t.Fatalf("expected 1 unpromoted note, got %+v", unpromoted)
	}

	decID, err := s.AddDecision("cache the thing", DecisionOpts{ProjectID: &projID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPromoted(id, "decision", decID); err != nil {
		t.Fatal(err)
	}

	unpromoted, err = s.ListNotes(NoteFilter{ProjectID: &projID, UnpromotedOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(unpromoted) != 0 {
		t.Errorf("note should no longer be unpromoted, got %+v", unpromoted)
	}

	n, err := s.GetNote(id)
	if err != nil {
		t.Fatal(err)
	}
	if !n.PromotedTo.Valid || n.PromotedTo.String != "decision" || n.PromotedID.Int64 != decID {
		t.Errorf("promotion not recorded: %+v", n)
	}
}

func TestSearchFindsDecisionsMemoryAndNotes(t *testing.T) {
	s := humanStore(t)
	if _, err := s.AddDecision("adopt sqlite fts5", DecisionOpts{Rationale: "built-in keyword search"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMemory("search", "constraint", "fts5 queries must be quoted if they contain punctuation"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddNote(nil, "fts5 snippet function needs a column index", "manual"); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("fts5", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits across decision/memory/note, got %d: %+v", len(hits), hits)
	}
}

// TestSearchStatusReflectsCurrentSourceRowState is the fix for the "FTS5
// index staleness on status change" finding: search_index itself is only
// ever populated once at insert time (see indexForSearch), so Status has to
// come from a fresh read of the source row at query time, not from
// anything cached in the FTS5 row — otherwise a rejected decision or a
// stale memory entry would keep surfacing with no signal that it's no
// longer authoritative.
func TestSearchStatusReflectsCurrentSourceRowState(t *testing.T) {
	s := humanStore(t)

	acceptedID, err := s.AddDecision("adopt widgets everywhere", DecisionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setDecisionStatus(acceptedID, "accepted"); err != nil {
		t.Fatal(err)
	}
	rejectedID, err := s.AddDecision("widgets turned out to be a mistake", DecisionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setDecisionStatus(rejectedID, "rejected"); err != nil {
		t.Fatal(err)
	}

	memID, err := s.AddMemory("widgets", "lesson", "widgets require careful cleanup")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetMemoryStale(memID, true); err != nil {
		t.Fatal(err)
	}

	promotedNoteID, err := s.AddNote(nil, "widgets: consider a decision here", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkPromoted(promotedNoteID, "decision", acceptedID); err != nil {
		t.Fatal(err)
	}
	unpromotedNoteID, err := s.AddNote(nil, "widgets don't need a decision yet", "manual")
	if err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search("widgets", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Keyed by (kind, id): decisions/memory/notes each have their own
	// independent id sequence, so raw ids collide across kinds.
	type key struct {
		kind string
		id   int64
	}
	statusOf := map[key]string{}
	for _, h := range hits {
		statusOf[key{h.Kind, h.RefID}] = h.Status
	}

	cases := []struct {
		name string
		kind string
		id   int64
		want string
	}{
		{"accepted decision", "decision", acceptedID, "accepted"},
		{"rejected decision", "decision", rejectedID, "rejected"},
		{"stale memory entry", "memory", memID, "stale"},
		{"promoted note", "note", promotedNoteID, "promoted"},
		{"unpromoted note", "note", unpromotedNoteID, ""},
	}
	for _, c := range cases {
		got, ok := statusOf[key{c.kind, c.id}]
		if !ok {
			t.Errorf("%s: expected a search hit for %s #%d, got none (hits: %+v)", c.name, c.kind, c.id, hits)
			continue
		}
		if got != c.want {
			t.Errorf("%s: expected status %q, got %q", c.name, c.want, got)
		}
	}
}

func TestSnapshotRoundTripRebuildsSearchIndex(t *testing.T) {
	src := humanStore(t)
	if _, err := src.AddDecision("adopt fts5 search", DecisionOpts{}); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := src.SnapshotJSON(&buf); err != nil {
		t.Fatal(err)
	}

	dst := humanStore(t)
	if _, err := dst.LoadSnapshot(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatal(err)
	}

	hits, err := dst.Search("fts5", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected the imported decision to be searchable, got %d hits", len(hits))
	}
}

// TestResolveCurrentProjectFallsBackWhenEnvNamesUnknownProject is the fix
// for a real bug found while wiring this repo up to track its own
// development: ACLINE_PROJECT (or a .acline-project marker — see the sibling
// test below) naming a project that doesn't exist in the currently-open db
// used to be a hard error from every project-scoped command, instead of
// falling back the way ResolveCurrentProject's own doc comment always
// promised. Concretely, this broke `go test ./internal/cmd/...` for anyone
// with a `.acline-project` marker committed at a repo's root: tests open an
// isolated temp db with no projects registered, but cwd resolution still
// walks up and finds the marker.
func TestResolveCurrentProjectFallsBackWhenEnvNamesUnknownProject(t *testing.T) {
	s := humanStore(t)
	t.Setenv("ACLINE_PROJECT", "does-not-exist-in-this-db")
	t.Chdir(t.TempDir())

	_, err := s.ResolveCurrentProject()
	if !errors.Is(err, ErrNoProject) {
		t.Fatalf("expected ErrNoProject (graceful fallback), got %v", err)
	}
}

func TestResolveCurrentProjectFallsBackWhenMarkerNamesUnknownProject(t *testing.T) {
	s := humanStore(t)
	dir := t.TempDir()
	if err := WriteMarker(dir, "does-not-exist-in-this-db"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	_, err := s.ResolveCurrentProject()
	if !errors.Is(err, ErrNoProject) {
		t.Fatalf("expected ErrNoProject (graceful fallback), got %v", err)
	}
}

// TestResolveCurrentProjectMarkerStillResolvesRealProject is the happy-path
// sibling of the two tests above: a marker naming a project that *does*
// exist in this db must still resolve to it, not just fall back.
func TestResolveCurrentProjectMarkerStillResolvesRealProject(t *testing.T) {
	s := humanStore(t)
	// A marker is only trusted inside the project's registered path (see
	// TestMarkerOutsideTheProjectsPathIsIgnored): here it sits in a subdirectory
	// of the project, the case WriteMarker exists for.
	root := t.TempDir()
	if _, err := s.AddProject("demo", root, "hotl"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(dir, "demo"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	p, err := s.ResolveCurrentProject()
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "demo" {
		t.Fatalf("expected the marker to resolve to %q, got %q", "demo", p.Name)
	}
}
