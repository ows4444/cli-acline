package cmd

import (
	"database/sql"
	"strings"
	"testing"

	"acline/internal/store"
)

func TestRenderClaudeMDIncludesAnswers(t *testing.T) {
	out := renderClaudeMD(initAnswers{
		ProjectName: "widget-tool",
		Areas:       []string{"backend", "frontend"},
		DefaultRisk: "medium",
		DefaultHITL: true,
	})
	for _, want := range []string{"# widget-tool", "backend, frontend", "risk=medium", "autonomy=hitl"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected rendered CLAUDE.md to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRenderClaudeMDLinksScaffoldOnlyWhenPresent(t *testing.T) {
	scaffolded := renderClaudeMD(initAnswers{Scaffold: true})
	for _, want := range []string{"## Memory layer", "MEMORY_LOG:", "/recall", "vault-structure", ".claude/vault/SOUL.md", "hook injects `acline dashboard`", "`acline note add \"...\"`"} {
		if !strings.Contains(scaffolded, want) {
			t.Errorf("expected scaffolded CLAUDE.md to contain %q, got:\n%s", want, scaffolded)
		}
	}

	bare := renderClaudeMD(initAnswers{})
	for _, bad := range []string{"## Memory layer", "MEMORY_LOG:", "hook injects"} {
		if strings.Contains(bare, bad) {
			t.Errorf("CLAUDE.md without a scaffold should not reference %q:\n%s", bad, bare)
		}
	}

	// `acline log --type note` never reaches /reflect, so neither variant
	// should suggest it.
	for _, out := range []string{scaffolded, bare} {
		if strings.Contains(out, "--type note") {
			t.Errorf("CLAUDE.md should not suggest `acline log --type note`:\n%s", out)
		}
	}
}

func TestRenderClaudeMDFallsBackOnEmptyAnswers(t *testing.T) {
	out := renderClaudeMD(initAnswers{})
	for _, want := range []string{"# this project", "risk=low", "autonomy=hotl", "none fixed"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected default fallback %q in output, got:\n%s", want, out)
		}
	}
}

func TestProgressStr(t *testing.T) {
	cases := []struct {
		p    store.MilestoneProgress
		want string
	}{
		{store.MilestoneProgress{Done: 2, Total: 5}, "2/5"},
		{store.MilestoneProgress{Done: 0, Total: 0}, "0/0"},
		{store.MilestoneProgress{Done: 3, Total: 5, Cancelled: 2}, "3/5 (2 cxl)"},
	}
	for _, c := range cases {
		if got := progressStr(c.p); got != c.want {
			t.Errorf("progressStr(%+v) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestActorShort(t *testing.T) {
	cases := []struct {
		in   sql.NullString
		want string
	}{
		{sql.NullString{String: "agent", Valid: true}, "agent"},
		{sql.NullString{String: "human", Valid: true}, "human"},
		{sql.NullString{}, "-"},
		{sql.NullString{String: "bot", Valid: true}, "-"},
	}
	for _, c := range cases {
		if got := actorShort(c.in); got != c.want {
			t.Errorf("actorShort(%+v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPrintImportSummary(t *testing.T) {
	out := captureStdout(t, func() {
		printImportSummary("acline.json", map[string]int{"tasks": 2, "specs": 1})
	})
	s := string(out)
	for _, want := range []string{"loaded acline.json: 3 row(s) added", "specs", "tasks"} {
		if !strings.Contains(s, want) {
			t.Errorf("expected output to contain %q, got %q", want, s)
		}
	}
	// Table names must print in sorted order regardless of map iteration order.
	if strings.Index(s, "specs") > strings.Index(s, "tasks") {
		t.Errorf("expected sorted table order (specs before tasks), got %q", s)
	}
}

func TestPrintImportSummaryNothingNew(t *testing.T) {
	out := captureStdout(t, func() {
		printImportSummary("acline.json", map[string]int{})
	})
	if !strings.Contains(string(out), "nothing new") {
		t.Errorf("expected a 'nothing new' message for an empty count map, got %q", out)
	}
}

func TestSplitTrim(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"backend, frontend, mobile", []string{"backend", "frontend", "mobile"}},
		{"  a  ,, b  ", []string{"a", "b"}},
		{"", nil},
	}
	for _, c := range cases {
		got := splitTrim(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitTrim(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitTrim(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestDefaultProjectNameUsesDirBase(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got := defaultProjectName()
	want := dir[strings.LastIndex(dir, "/")+1:]
	if got != want {
		t.Errorf("defaultProjectName() = %q, want the temp dir's base name %q", got, want)
	}
}

func TestRenderClaudeMDDoesNotHardcodeActorOrModel(t *testing.T) {
	out := renderClaudeMD(initAnswers{})
	for _, bad := range []string{"export ACLINE_", "claude-sonnet", "claude-opus"} {
		if strings.Contains(out, bad) {
			t.Errorf("rendered CLAUDE.md should not contain %q:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "acline memory decay") {
		t.Errorf("expected the memory decay guidance, got:\n%s", out)
	}
}
