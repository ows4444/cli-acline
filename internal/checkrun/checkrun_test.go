package checkrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir, kind, cmd string, to time.Duration) Result {
	t.Helper()
	r, err := Run(context.Background(), kind, dir, cmd, to)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExitCodeDecidesPassOrFail(t *testing.T) {
	d := t.TempDir()
	if r := run(t, d, "sast", "true", 0); r.Status != "pass" {
		t.Errorf("true = %+v", r)
	}
	// No shell: ";" is a literal argument, so "false" never runs.
	if r := run(t, d, "sast", "echo a; false", 0); r.Status != "pass" || !strings.Contains(r.Detail, "a; false") {
		t.Errorf("shell metacharacters were interpreted: %+v", r)
	}
	if r := run(t, d, "sca", "false", 0); r.Status != "fail" || !strings.Contains(r.Detail, "exit 1") {
		t.Errorf("false = %+v", r)
	}
}

func TestOutputTailIsKeptInDetail(t *testing.T) {
	r := run(t, t.TempDir(), "lint", "echo found 3 issues", 0)
	if r.Status != "pass" || !strings.Contains(r.Detail, "found 3 issues") {
		t.Errorf("result = %+v", r)
	}
}

func TestMissingToolIsSkippedNeverPass(t *testing.T) {
	r := run(t, t.TempDir(), "sast", "no-such-tool-xyz ./...", 0)
	if r.Status != "skipped" || !strings.Contains(r.Detail, "not installed") {
		t.Errorf("result = %+v", r)
	}
}

func TestNoDefaultRunnerOutsideAGoProject(t *testing.T) {
	if r := run(t, t.TempDir(), "sca", "", 0); r.Status != "skipped" {
		t.Errorf("no go.mod: %+v", r)
	}
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "go.mod"), []byte("module x\n"), 0o644)
	// with go.mod the default is used; govulncheck is either absent (skipped)
	// or runs -- either way it must not be skipped for lack of a runner
	if r := run(t, d, "sca", "", 0); strings.Contains(r.Detail, "no default") {
		t.Errorf("go.mod project got no default: %+v", r)
	}
}

func TestTimeoutFails(t *testing.T) {
	r := run(t, t.TempDir(), "test", "sleep 5", 100*time.Millisecond)
	if r.Status != "fail" || !strings.Contains(r.Detail, "timed out") {
		t.Errorf("result = %+v", r)
	}
}

func TestUnknownKindIsAnError(t *testing.T) {
	if _, err := Run(context.Background(), "human_review", t.TempDir(), "", 0); err == nil {
		t.Error("human_review has no runner")
	}
}

// The runner command was split on whitespace, so a quoted argument such as
// `-run "TestA|TestB"` could not be expressed. Quotes group now; there is still
// no shell, so metacharacters stay literal.
func TestSplitCommandHonoursQuotesWithoutAShell(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"go test ./...", []string{"go", "test", "./..."}},
		{`go test -run "TestA|TestB" ./...`, []string{"go", "test", "-run", "TestA|TestB", "./..."}},
		{`sh -c 'echo $HOME; false'`, []string{"sh", "-c", "echo $HOME; false"}},
		{`echo a\ b "c \"d\""`, []string{"echo", "a b", `c "d"`}},
		{`echo a; false`, []string{"echo", "a;", "false"}},
		{`  spaced   out  `, []string{"spaced", "out"}},
		{`echo ""`, []string{"echo", ""}},
	} {
		got, err := SplitCommand(tc.in)
		if err != nil || strings.Join(got, "\x00") != strings.Join(tc.want, "\x00") || len(got) != len(tc.want) {
			t.Errorf("SplitCommand(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{`echo "unterminated`, `echo 'x`, `echo x\`} {
		if _, err := SplitCommand(bad); err == nil {
			t.Errorf("SplitCommand(%q): expected an error", bad)
		}
	}
	if r := run(t, t.TempDir(), "test", `echo "a  b"`, 0); r.Status != "pass" || !strings.Contains(r.Detail, "a  b") {
		t.Errorf("a quoted argument was split: %+v", r)
	}
}

// Outside Go every runnable kind was "skipped", which blocks high risk,
// and nothing said what to configure.
func TestSuggestRunnersDetectsTheEcosystemButNotGo(t *testing.T) {
	d := t.TempDir()
	if eco, s := SuggestRunners(d); eco != "" || s != nil {
		t.Fatalf("an empty directory = %q, %v", eco, s)
	}
	os.WriteFile(filepath.Join(d, "package.json"), []byte("{}"), 0o644)
	eco, s := SuggestRunners(d)
	if eco != "Node.js" || len(s) == 0 || s[0].Kind != "test" {
		t.Fatalf("package.json = %q, %v", eco, s)
	}
	os.WriteFile(filepath.Join(d, "go.mod"), []byte("module x\n"), 0o644)
	if eco, s := SuggestRunners(d); eco != "" || s != nil {
		t.Fatalf("a Go module has defaults; got %q, %v", eco, s)
	}
}
