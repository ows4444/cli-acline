package tui

import (
	"errors"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ows4444/tui/tuitest"

	"acline/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	st.Actor = store.Actor{Type: "human", ID: "ana"}
	return st
}

// noHash stands in for worktree.Hash: no tree is known.
func noHash(string) string { return "" }

func rendered(s *tuitest.Session) string { return strings.Join(s.Screen(), "\n") }

func TestShellShowsWhoIsActingAndWhetherItIsEnforced(t *testing.T) {
	st := openTestStore(t)
	m, err := newModel(st, nil, noHash)
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 120, 20)
	defer s.Close()
	got := rendered(s)
	for _, want := range []string{"acline · all projects · human/ana", "token: OFF", "no active session", "Audit trail", "intact: 0 event(s)", "Nothing waiting: Awaiting your approval, Tasks needing attention"} {
		if !strings.Contains(got, want) {
			t.Errorf("screen lacks %q:\n%s", want, got)
		}
	}
}

func TestQuitsOnQ(t *testing.T) {
	m, err := newModel(openTestStore(t), nil, noHash)
	if err != nil {
		t.Fatal(err)
	}
	s := tuitest.New(m, 80, 10)
	defer s.Close()
	s.Keys("q")
	for end := time.Now().Add(5 * time.Second); !s.Done() && time.Now().Before(end); {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.Done() {
		t.Fatal("q did not quit")
	}
}

func TestRunRefusesAnAgent(t *testing.T) {
	st := openTestStore(t)
	st.Actor = store.Actor{Type: "agent", ID: "claude-code"}
	err := Run(Options{Store: st, In: strings.NewReader("q"), Out: io.Discard})
	if !errors.Is(err, ErrAgent) {
		t.Fatalf("Run as an agent = %v, want ErrAgent", err)
	}
}

// The rest of acline reaches the library only through this package, so a
// change of library touches one package.
func TestOnlyThisPackageImportsTheLibrary(t *testing.T) {
	root := filepath.Join("..", "..")
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || path == filepath.Join(root, "internal", "tui") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "github.com/ows4444/tui" || strings.HasPrefix(p, "github.com/ows4444/tui/") {
				bad = append(bad, path+": "+p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("only internal/tui may import the TUI library:\n%s", strings.Join(bad, "\n"))
	}
}

// Every screen of the real shell opens, on an empty store and on one with
// something of each kind in it, by its number and by tab.
func TestEveryScreenOpensFromEveryOther(t *testing.T) {
	titles := []string{"Dashboard", "Tasks", "Review", "Specs", "Memory", "Audit", "Orchestrator"}
	for name, seed := range map[string]func(*testing.T, *store.Store){"empty": func(*testing.T, *store.Store) {}, "seeded": seedReview} {
		t.Run(name, func(t *testing.T) {
			st := openTestStore(t)
			seed(t, st)
			m, err := newModel(st, nil, noHash)
			if err != nil {
				t.Fatal(err)
			}
			s := tuitest.New(m, 140, 40)
			t.Cleanup(s.Close)
			for i, title := range titles {
				s.Keys(strconv.Itoa(i + 1))
				shows(t, s, "["+title+"]")
			}
			for i := range titles { // tab wraps from the last to the first
				s.Keys("tab")
				shows(t, s, "["+titles[i]+"]")
			}
			for i := len(titles) - 2; i >= 0; i-- {
				s.Keys("shift+tab")
				shows(t, s, "["+titles[i]+"]")
			}
		})
	}
}
