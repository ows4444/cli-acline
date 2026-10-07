package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/ows4444/tui/tuitest"
)

// The golden frames pin the whole layout of the two screens a person decides
// from. Accept an intended change with TUITEST_UPDATE=1 go test ./internal/tui/.

var timestamp = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ`)

// golden compares the screen with testdata/<name>.golden. Times are the
// store's own clock, so each is replaced by one of the same width.
func golden(t *testing.T, s *tuitest.Session, name string) {
	t.Helper()
	got := timestamp.ReplaceAllString(rendered(s), "2026-01-01T00:00:00Z") + "\n"
	path := filepath.Join("testdata", name+".golden")
	if os.Getenv("TUITEST_UPDATE") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden (TUITEST_UPDATE=1 creates it): %v", err)
	}
	if string(want) != got {
		t.Errorf("screen differs from %s (TUITEST_UPDATE=1 accepts)\n--- want\n%s--- got\n%s", path, want, got)
	}
}

func TestDashboardGolden(t *testing.T) {
	st := openTestStore(t)
	seedReview(t, st)
	m, err := newModel(st, nil, func(string) string { return "sha256:now" })
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 120, 40)
	t.Cleanup(s.Close)
	golden(t, s, "dashboard")
}

func TestTaskDetailGolden(t *testing.T) {
	s, _ := detailSession(t, 120, 40)
	golden(t, s, "task-detail")
}
