package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"acline/internal/store"
)

func TestAskReturnsDefaultOnEmptyInput(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	out := captureStdout(t, func() {
		if got := ask(r, "Project name", "fallback"); got != "fallback" {
			t.Errorf("ask() = %q, want default %q", got, "fallback")
		}
	})
	if !strings.Contains(string(out), "[fallback]") {
		t.Errorf("expected the default to be shown in the prompt, got %q", out)
	}
}

func TestAskReturnsTypedInput(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("widget-tool\n"))
	captureStdout(t, func() {
		if got := ask(r, "Project name", "fallback"); got != "widget-tool" {
			t.Errorf("ask() = %q, want %q", got, "widget-tool")
		}
	})
}

func TestAskYesNo(t *testing.T) {
	cases := []struct {
		input string
		def   bool
		want  bool
	}{
		{"\n", false, false},
		{"\n", true, true},
		{"y\n", false, true},
		{"yes\n", false, true},
		{"n\n", true, false},
		{"no\n", true, false},
		{"Y\n", false, true},
		{"garbage\n", true, true}, // falls back to the default on anything unrecognized
	}
	for _, c := range cases {
		r := bufio.NewReader(strings.NewReader(c.input))
		captureStdout(t, func() {
			if got := askYesNo(r, "Question", c.def); got != c.want {
				t.Errorf("askYesNo(%q, def=%v) = %v, want %v", c.input, c.def, got, c.want)
			}
		})
	}
}

func TestIsInteractiveUnderTestRunner(t *testing.T) {
	// `go test`'s stdin is never an attached terminal, so this should
	// reliably report false without needing to fake a tty.
	if isInteractive() {
		t.Skip("stdin is a tty in this environment -- skipping, this is an environmental fact this test can't control")
	}
}

func TestGatherInitAnswersUsesFlagsWhenSet(t *testing.T) {
	prevName, prevRisk, prevHITL, prevAreas, prevYes := initName, initRisk, initHITL, initAreas, initYes
	t.Cleanup(func() {
		initName, initRisk, initHITL, initAreas, initYes = prevName, prevRisk, prevHITL, prevAreas, prevYes
	})

	initName = "widget-tool"
	initRisk = "high"
	initHITL = true
	initAreas = "backend, frontend"
	initYes = true

	a := gatherInitAnswers(true)
	if a.ProjectName != "widget-tool" || a.DefaultRisk != "high" || !a.DefaultHITL {
		t.Errorf("expected flags to pass through unchanged, got %+v", a)
	}
	if len(a.Areas) != 2 || a.Areas[0] != "backend" || a.Areas[1] != "frontend" {
		t.Errorf("expected areas split from the flag, got %v", a.Areas)
	}
}

func TestGatherInitAnswersFallsBackNonInteractively(t *testing.T) {
	prevName, prevRisk, prevHITL, prevAreas, prevYes := initName, initRisk, initHITL, initAreas, initYes
	t.Cleanup(func() {
		initName, initRisk, initHITL, initAreas, initYes = prevName, prevRisk, prevHITL, prevAreas, prevYes
	})

	initName, initRisk, initHITL, initAreas = "", "", false, ""
	initYes = true
	dir := t.TempDir()
	t.Chdir(dir)

	a := gatherInitAnswers(true)
	if a.DefaultRisk != "low" {
		t.Errorf("expected default risk to fall back to low, got %q", a.DefaultRisk)
	}
	if a.ProjectName == "" {
		t.Error("expected ProjectName to fall back to detectProjectName(), got empty")
	}
}

func TestWriteClaudeMDSkipsExistingFileWithoutForce(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("CLAUDE.md", []byte("hand-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevForce := initForceClaude
	initForceClaude = false
	t.Cleanup(func() { initForceClaude = prevForce })

	out := captureStdout(t, func() {
		if _, err := writeClaudeMD(false); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(string(out), "leaving it alone") {
		t.Errorf("expected a 'leaving it alone' message, got %q", out)
	}
	got, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hand-written\n" {
		t.Fatalf("existing CLAUDE.md was overwritten: %q", got)
	}
}

func TestWriteClaudeMDWritesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	prevName, prevRisk, prevYes, prevForce := initName, initRisk, initYes, initForceClaude
	initName, initRisk, initYes, initForceClaude = "widget-tool", "low", true, false
	t.Cleanup(func() { initName, initRisk, initYes, initForceClaude = prevName, prevRisk, prevYes, prevForce })

	if _, err := writeClaudeMD(false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("CLAUDE.md")
	if err != nil {
		t.Fatalf("expected CLAUDE.md to be written: %v", err)
	}
	if !strings.Contains(string(got), "widget-tool") {
		t.Errorf("expected the generated CLAUDE.md to mention the project name, got:\n%s", got)
	}
}

func TestLoadSnapshotSeedNoopWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := withTestStore(t)
	prevFromJSON := initFromJSON
	initFromJSON = "does-not-exist.json"
	t.Cleanup(func() { initFromJSON = prevFromJSON })

	if err := loadSnapshotSeed(s); err != nil {
		t.Fatalf("expected a missing snapshot file to be a no-op, got: %v", err)
	}
}

func TestLoadSnapshotSeedLoadsPresentFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	src := withTestStore(t)
	if _, err := src.AddTask("seed task", "", "normal", store.TaskOpts{}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create("seed.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.SnapshotJSON(f); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dst := withTestStore(t)
	prevFromJSON := initFromJSON
	initFromJSON = "seed.json"
	t.Cleanup(func() { initFromJSON = prevFromJSON })

	out := captureStdout(t, func() {
		if err := loadSnapshotSeed(dst); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(string(out), "row(s) added") {
		t.Errorf("expected an import summary to be printed, got %q", out)
	}
	tasks, err := dst.ListTasks(store.TaskFilter{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Title != "seed task" {
		t.Fatalf("expected the seeded task to be imported, got %+v", tasks)
	}
}

func TestRegisterCurrentProjectRegistersAndMarksCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	s := withTestStore(t)
	cwd, _ := os.Getwd()

	if err := registerCurrentProject(s, "widget-tool", true); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetProjectByName("widget-tool")
	if err != nil {
		t.Fatalf("expected project to be registered: %v", err)
	}
	if p.Path.String != cwd || p.AutonomyDefault != "hitl" {
		t.Errorf("got path=%q autonomy=%q, want %q hitl", p.Path.String, p.AutonomyDefault, cwd)
	}
	cur, err := s.ResolveCurrentProject()
	if err != nil || cur.Name != "widget-tool" {
		t.Fatalf("expected widget-tool to be the current project, got %v, %v", cur, err)
	}

	// Re-running init in the same directory must not register it twice,
	// even under a different name.
	out := captureStdout(t, func() {
		if err := registerCurrentProject(s, "renamed", false); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(string(out), "already registered") {
		t.Errorf("expected an 'already registered' message, got %q", out)
	}
	projects, _ := s.ListProjects()
	if len(projects) != 1 {
		t.Fatalf("expected one project, got %d", len(projects))
	}
}

func TestRegisterCurrentProjectLeavesNameConflictAlone(t *testing.T) {
	s := withTestStore(t)
	if _, err := s.AddProject("widget-tool", t.TempDir(), "hotl"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	out := captureStdout(t, func() {
		if err := registerCurrentProject(s, "widget-tool", false); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(string(out), "already registered at") {
		t.Errorf("expected a name-conflict warning, got %q", out)
	}
	projects, _ := s.ListProjects()
	if len(projects) != 1 {
		t.Fatalf("name conflict should not add a project, got %d", len(projects))
	}
}

// `acline init` used to leave identity self-declared without saying so, while
// the generated CLAUDE.md told agents they cannot self-approve.
func TestInitOffersTheApprovalToken(t *testing.T) {
	yes := func(string) bool { return true }
	no := func(string) bool { return false }
	never := func(string) bool { t.Fatal("asked in a non-interactive run"); return false }

	t.Run("a script is told how, and nothing is enabled", func(t *testing.T) {
		s := withTestStore(t)
		out := string(captureStdout(t, func() {
			if err := offerApprovalToken(s, false, never); err != nil {
				t.Fatal(err)
			}
		}))
		if !strings.Contains(out, "acline auth init") || !strings.Contains(out, "self-declared") {
			t.Errorf("hint missing: %q", out)
		}
		if on, _ := s.ApprovalTokenEnabled(); on {
			t.Fatal("token enabled without a person")
		}
	})

	t.Run("declining leaves it off and init succeeds", func(t *testing.T) {
		s := withTestStore(t)
		captureStdout(t, func() {
			if err := offerApprovalToken(s, true, no); err != nil {
				t.Fatal(err)
			}
		})
		if on, _ := s.ApprovalTokenEnabled(); on {
			t.Fatal("token enabled after declining")
		}
	})

	t.Run("accepting enables it after the terminal confirmation", func(t *testing.T) {
		s := withTestStore(t)
		tty := withFakeTerminal(t, "ENABLE\n")
		captureStdout(t, func() {
			if err := offerApprovalToken(s, true, yes); err != nil {
				t.Fatal(err)
			}
		})
		if on, _ := s.ApprovalTokenEnabled(); !on {
			t.Fatal("token not enabled after accepting")
		}
		if !strings.Contains(tty.out.String(), "acl_") {
			t.Errorf("token not shown on the terminal: %q", tty.out.String())
		}
	})

	t.Run("no terminal: init still succeeds, token stays off", func(t *testing.T) {
		s := withTestStore(t)
		withNoTerminal(t)
		captureStdout(t, func() {
			if err := offerApprovalToken(s, true, yes); err != nil {
				t.Fatalf("init must not fail over the offer: %v", err)
			}
		})
		if on, _ := s.ApprovalTokenEnabled(); on {
			t.Fatal("token enabled without a terminal")
		}
	})

	t.Run("already enabled: silent", func(t *testing.T) {
		s := withTestStore(t)
		if _, err := s.EnableApprovalToken(); err != nil {
			t.Fatal(err)
		}
		out := captureStdout(t, func() {
			if err := offerApprovalToken(s, true, never); err != nil {
				t.Fatal(err)
			}
		})
		if len(out) != 0 {
			t.Errorf("printed %q when a token is already enabled", out)
		}
	})
}

// `acline init` wrote per-machine state into the project without ignoring it.
func TestEnsureGitignoredAppendsOnlyWhatIsMissing(t *testing.T) {
	dir := t.TempDir()
	if added, err := ensureGitignored(dir, initGitignoreEntries); err != nil || added != nil {
		t.Fatalf("not a git checkout: %v, %v", added, err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules/\n/.acline-project"), 0o644)
	added, err := ensureGitignored(dir, initGitignoreEntries)
	if err != nil || len(added) != 1 || added[0] != ".claude/vault/.state/" {
		t.Fatalf("added = %v, %v", added, err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if want := "node_modules/\n/.acline-project\n\n# acline: per-machine state\n.claude/vault/.state/\n"; string(got) != want {
		t.Fatalf(".gitignore = %q, want %q", got, want)
	}
	if again, _ := ensureGitignored(dir, initGitignoreEntries); again != nil {
		t.Fatalf("second run added %v", again)
	}
}
