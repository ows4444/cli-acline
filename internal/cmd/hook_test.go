package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// These port the behaviours the Python hook scripts (and their unittest suite)
// used to guarantee, now that the hooks are `acline hook <event>`.

type selfCall struct {
	dir  string
	args []string
}

// fakeSelf replaces the subprocess `acline hook` runs to call itself.
func fakeSelf(t *testing.T, reply func(args []string) (string, error)) *[]selfCall {
	t.Helper()
	var calls []selfCall
	prev := runSelf
	runSelf = func(dir string, args ...string) (string, error) {
		calls = append(calls, selfCall{dir, args})
		return reply(args)
	}
	t.Cleanup(func() { runSelf = prev })
	return &calls
}

// --- pre-tool-use: the guard passthrough, failing closed ---

func TestPreToolUseForwardsADenyDecisionVerbatim(t *testing.T) {
	withTestStore(t)
	var out bytes.Buffer
	if err := runPreToolUse(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"rm -rf /"}}`), &out); err != nil {
		t.Fatal(err)
	}
	var d hookDenyOutput
	if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("output = %q (%v)", out.String(), err)
	}
}

func TestPreToolUseAllowProducesNoOutput(t *testing.T) {
	withTestStore(t)
	var out bytes.Buffer
	if err := runPreToolUse(strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"go test ./..."}}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("an allowed call produced output: %q", out.String())
	}
}

func TestPreToolUseEmptyStdinResolves(t *testing.T) {
	withTestStore(t)
	var out bytes.Buffer
	if err := runPreToolUse(strings.NewReader(""), &out); err != nil || out.Len() != 0 {
		t.Fatalf("empty stdin: %v, %q", err, out.String())
	}
}

func TestPreToolUseFailsClosedWhenTheGuardErrorsPanicsOrHangs(t *testing.T) {
	for name, guard := range map[string]func(io.Reader, io.Writer) error{
		"error": func(io.Reader, io.Writer) error { return errors.New("the store is unavailable") },
		"panic": func(io.Reader, io.Writer) error { panic("boom") },
		"hang":  func(io.Reader, io.Writer) error { time.Sleep(2 * time.Second); return nil },
	} {
		t.Run(name, func(t *testing.T) {
			prevGuard, prevTimeout := guardRun, hookTimeout
			guardRun, hookTimeout = guard, 100*time.Millisecond
			t.Cleanup(func() { guardRun, hookTimeout = prevGuard, prevTimeout })
			var out bytes.Buffer
			if err := runPreToolUse(strings.NewReader(`{"tool_name":"Bash"}`), &out); err != nil {
				t.Fatal(err)
			}
			var d hookDenyOutput
			if err := json.Unmarshal(out.Bytes(), &d); err != nil || d.HookSpecificOutput.PermissionDecision != "deny" {
				t.Fatalf("a guard that %ss must deny, got %q", name, out.String())
			}
			if !strings.Contains(d.HookSpecificOutput.PermissionDecisionReason, "blocked") {
				t.Errorf("reason = %q", d.HookSpecificOutput.PermissionDecisionReason)
			}
		})
	}
}

// --- session-start ---

func TestSessionStartExportsTheModelForLaterAclineCalls(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "env.sh")
	export := func(payload map[string]any, file string) {
		exportModel(payload, file)
	}
	export(map[string]any{"model": "claude-opus-5-5"}, envFile)
	if got, _ := os.ReadFile(envFile); string(got) != "export ACLINE_MODEL=claude-opus-5-5\n" {
		t.Errorf("plain model = %q", got)
	}
	os.Remove(envFile)
	export(map[string]any{"model": map[string]any{"id": "claude-sonnet-5", "display_name": "Sonnet 5"}}, envFile)
	if got, _ := os.ReadFile(envFile); string(got) != "export ACLINE_MODEL=claude-sonnet-5\n" {
		t.Errorf("model object = %q", got)
	}
	os.Remove(envFile)
	export(map[string]any{"model": "x; rm y"}, envFile)
	if got, _ := os.ReadFile(envFile); string(got) != "export ACLINE_MODEL='x; rm y'\n" {
		t.Errorf("metacharacters must be quoted, got %q", got)
	}
	os.Remove(envFile)
	export(map[string]any{}, envFile)
	export(map[string]any{"model": "claude-opus-5-5"}, "")
	if _, err := os.Stat(envFile); err == nil {
		t.Error("no model, or no env file, must be a no-op")
	}
}

func TestShellQuoteMatchesWhatAShellNeeds(t *testing.T) {
	for in, want := range map[string]string{
		"claude-opus-5-5": "claude-opus-5-5", "a.b/c:d@e%f+g=h,i": "a.b/c:d@e%f+g=h,i", "": "''",
		"has space": "'has space'", "it's": `'it'"'"'s'`, "$(x)": "'$(x)'", "a;b": "'a;b'",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionStartRunsFromTheProjectRootAndWrapsTheDashboard(t *testing.T) {
	project := t.TempDir()
	calls := fakeSelf(t, func(args []string) (string, error) {
		if args[0] == "dashboard" {
			return "open tasks: 1\n", nil
		}
		return "# Project context\n", nil
	})
	var out bytes.Buffer
	if err := runSessionStart(strings.NewReader(`{"model":"m"}`), &out, project, ""); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("calls = %+v, want dashboard and context export", *calls)
	}
	for _, c := range *calls {
		if c.dir != project {
			t.Errorf("%v ran from %q, want the project root %q", c.args, c.dir, project)
		}
	}
	var got struct {
		Hook struct {
			Name    string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Hook.Name != "SessionStart" || !strings.Contains(got.Hook.Context, "# Dashboard\n\n```\nopen tasks: 1\n```") || !strings.Contains(got.Hook.Context, "# Project context") {
		t.Fatalf("context = %q", got.Hook.Context)
	}
	if strings.Index(got.Hook.Context, "# Dashboard") > strings.Index(got.Hook.Context, "# Project context") {
		t.Error("the dashboard comes first")
	}
}

func TestSessionStartInjectsAnUnfinishedBootstrapAndReportsFailuresWithoutRaising(t *testing.T) {
	project := t.TempDir()
	os.MkdirAll(filepath.Join(project, ".claude", "vault"), 0o755)
	os.WriteFile(filepath.Join(project, ".claude", "vault", "BOOTSTRAP.md"), []byte("Welcome, first run."), 0o644)
	fakeSelf(t, func(args []string) (string, error) { return "", errors.New("exit status 1") })
	var out bytes.Buffer
	if err := runSessionStart(strings.NewReader(""), &out, project, ""); err != nil {
		t.Fatalf("a failing acline must not fail the hook: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "ONBOARDING IN PROGRESS") || !strings.Contains(s, "Welcome, first run.") {
		t.Errorf("BOOTSTRAP.md was not injected: %s", s)
	}
	if !strings.Contains(s, "acline dashboard failed") {
		t.Errorf("the failure should be reported in the context: %s", s)
	}
}

// --- MEMORY_LOG capture (pre-compact and session-end) ---

func assistantEntry(text string) map[string]any {
	return map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}}
}

func writeTranscript(t *testing.T, entries ...any) string {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, _ := json.Marshal(e)
		b.Write(line)
		b.WriteByte('\n')
	}
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractMemoryLogLines(t *testing.T) {
	x := func(entries ...any) []string { return extractMemoryLogLines(writeTranscript(t, entries...)) }
	eq := func(name string, got, want []string) {
		t.Helper()
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	eq("single", x(assistantEntry("MEMORY_LOG: a fact")), []string{"a fact"})
	eq("order across messages", x(assistantEntry("MEMORY_LOG: one\nprose\nMEMORY_LOG: two"), assistantEntry("MEMORY_LOG: three")), []string{"one", "two", "three"})
	eq("dedup keeps first", x(assistantEntry("MEMORY_LOG: same"), assistantEntry("MEMORY_LOG: other"), assistantEntry("MEMORY_LOG: same")), []string{"same", "other"})
	eq("quotes kept", x(assistantEntry(`MEMORY_LOG: config key is "db.path"`)), []string{`config key is "db.path"`})
	eq("string content", x(map[string]any{"type": "assistant", "message": map[string]any{"content": "MEMORY_LOG: plain string content"}}), []string{"plain string content"})
	eq("tag must start its line", x(assistantEntry("Tag facts as `MEMORY_LOG: <fact>` when useful.")), nil)
	eq("no tag", x(assistantEntry("nothing tagged here")), nil)

	soul := "    MEMORY_LOG: <the fact, in one line, self-contained>"
	eq("ignores injected context, user messages and tool results", x(
		map[string]any{"type": "system", "content": soul},
		map[string]any{"type": "attachment", "attachment": map[string]any{"content": soul}},
		map[string]any{"type": "user", "message": map[string]any{"content": "MEMORY_LOG: typed by the user"}},
		map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "MEMORY_LOG: from a file the agent read"}}}},
	), nil)
	eq("ignores tool_use blocks", x(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": "echo 'MEMORY_LOG: nope'"}},
		map[string]any{"type": "text", "text": "MEMORY_LOG: yes"},
	}}}), []string{"yes"})

	p := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(p, []byte("not json\n"+func() string { b, _ := json.Marshal(assistantEntry("MEMORY_LOG: survives")); return string(b) }()+"\n"), 0o644)
	eq("malformed lines skipped", extractMemoryLogLines(p), []string{"survives"})
	eq("missing transcript", extractMemoryLogLines("/does/not/exist.jsonl"), nil)
	eq("empty path", extractMemoryLogLines(""), nil)
}

func TestCaptureSendsOnlyNewLinesAndTracksSessionsIndependently(t *testing.T) {
	project := t.TempDir()
	calls := fakeSelf(t, func([]string) (string, error) { return "", nil })
	state := filepath.Join(project, ".claude", "vault", ".state", "memory-log-captured.json")
	notes := func() []string {
		var out []string
		for _, c := range *calls {
			if c.args[0] == "note" {
				out = append(out, c.args[2])
				if c.dir != project {
					t.Errorf("note recorded from %q, want the project root", c.dir)
				}
				if c.args[3] != "--source" || c.args[4] != "conversation" {
					t.Errorf("args = %v", c.args)
				}
			}
		}
		return out
	}

	p := writeTranscript(t, assistantEntry("MEMORY_LOG: one"), assistantEntry("MEMORY_LOG: two"))
	captureMemoryLog("s1", p, state, project)
	if got := notes(); strings.Join(got, ",") != "one,two" {
		t.Fatalf("first capture sent %v", got)
	}
	captureMemoryLog("s1", p, state, project) // PreCompact then SessionEnd: nothing new
	if got := notes(); len(got) != 2 {
		t.Fatalf("a second capture with no new lines sent %v", got)
	}
	p2 := writeTranscript(t, assistantEntry("MEMORY_LOG: one"), assistantEntry("MEMORY_LOG: two"), assistantEntry("MEMORY_LOG: three"))
	captureMemoryLog("s1", p2, state, project)
	if got := notes(); strings.Join(got, ",") != "one,two,three" {
		t.Fatalf("only the delta should be sent, got %v", got)
	}
	captureMemoryLog("other-session", p2, state, project) // sessions are independent
	if got := notes(); len(got) != 6 {
		t.Fatalf("a different session should send its own lines, got %v", got)
	}
	saved := loadMemoryLogState(state)
	if len(saved["s1"].Seen) != 3 || len(saved["other-session"].Seen) != 3 || saved["s1"].Offset != fileSize(p2) {
		data, _ := os.ReadFile(state)
		t.Fatalf("state = %s", data)
	}
}

func TestCaptureToleratesAnUnreadableStateFile(t *testing.T) {
	project := t.TempDir()
	calls := fakeSelf(t, func([]string) (string, error) { return "", nil })
	state := filepath.Join(project, "state.json")
	os.WriteFile(state, []byte("{corrupt"), 0o644)
	captureMemoryLog("s", writeTranscript(t, assistantEntry("MEMORY_LOG: x")), state, project)
	if len(*calls) != 1 {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestPreCompactAndSessionEndShareOneEntryPointFromAnUnrelatedCwd(t *testing.T) {
	project := t.TempDir()
	t.Chdir(t.TempDir()) // the session's shell has cd'd somewhere else
	t.Setenv("CLAUDE_PROJECT_DIR", project)
	calls := fakeSelf(t, func([]string) (string, error) { return "", nil })
	p := writeTranscript(t, assistantEntry("MEMORY_LOG: from another cwd"))
	payload := `{"session_id":"s","transcript_path":"` + p + `"}`
	if err := runMemoryLogHook(strings.NewReader(payload), project); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].dir != project {
		t.Fatalf("calls = %+v", *calls)
	}
	if _, err := os.Stat(filepath.Join(project, ".claude", "vault", ".state", "memory-log-captured.json")); err != nil {
		t.Errorf("state not written under the project: %v", err)
	}
}

// The dashboard and the whole context export went into every session with
// no bound, so the start-of-session cost grew with the store.
func TestSessionStartContextIsBounded(t *testing.T) {
	project := t.TempDir()
	huge := strings.Repeat("é", 40<<10) // multibyte, so a byte cut could split a character
	calls := fakeSelf(t, func(args []string) (string, error) {
		return huge, nil
	})
	var out bytes.Buffer
	if err := runSessionStart(strings.NewReader(`{}`), &out, project, ""); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hook struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if n := len(got.Hook.Context); n > maxDashboardBytes+maxContextBytes+2048 {
		t.Fatalf("session-start context is %d bytes", n)
	}
	if !utf8.ValidString(got.Hook.Context) || !strings.Contains(got.Hook.Context, "run `acline context export` for all of it") {
		t.Fatalf("truncation is not marked or split a character")
	}
	var sawRows bool
	for _, c := range *calls {
		if len(c.args) > 1 && c.args[0] == "context" && slices.Contains(c.args, "--max-rows") {
			sawRows = true
		}
	}
	if !sawRows {
		t.Errorf("context export was not limited: %+v", *calls)
	}
}

// Each Stop re-read the whole transcript. Now a turn reads only what was
// appended, leaves a half-written last line for the next turn, and state in
// the old count-only format still prevents duplicates.
func TestCaptureReadsOnlyWhatWasAppended(t *testing.T) {
	project := t.TempDir()
	var sent []string
	fakeSelf(t, func(args []string) (string, error) {
		if args[0] == "note" {
			sent = append(sent, args[2])
		}
		return "", nil
	})
	state := filepath.Join(project, "state.json")
	p := writeTranscript(t, assistantEntry("MEMORY_LOG: one"))
	captureMemoryLog("s", p, state, project)

	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	two, _ := json.Marshal(assistantEntry("MEMORY_LOG: two"))
	three, _ := json.Marshal(assistantEntry("MEMORY_LOG: three"))
	f.Write(append(two, '\n'))
	f.Write(three[:len(three)/2]) // still being written
	f.Close()
	captureMemoryLog("s", p, state, project)
	if strings.Join(sent, ",") != "one,two" {
		t.Fatalf("sent %v", sent)
	}
	f, _ = os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.Write(append(three[len(three)/2:], '\n'))
	f.Close()
	captureMemoryLog("s", p, state, project)
	if strings.Join(sent, ",") != "one,two,three" {
		t.Fatalf("the completed line was not captured once: %v", sent)
	}
}

func TestCaptureHonoursTheOldCountOnlyState(t *testing.T) {
	project := t.TempDir()
	var sent []string
	fakeSelf(t, func(args []string) (string, error) {
		if args[0] == "note" {
			sent = append(sent, args[2])
		}
		return "", nil
	})
	state := filepath.Join(project, "state.json")
	os.WriteFile(state, []byte(`{"s": 2}`), 0o644) // two lines already sent by an older acline
	p := writeTranscript(t, assistantEntry("MEMORY_LOG: one"), assistantEntry("MEMORY_LOG: two"), assistantEntry("MEMORY_LOG: three"))
	captureMemoryLog("s", p, state, project)
	if strings.Join(sent, ",") != "three" {
		t.Fatalf("sent %v, want only the line the old state had not sent", sent)
	}
}
