package cmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"acline/internal/clip"
)

// hook.go is Claude Code's hook surface, as `acline hook <event>`. These used to be
// four Python scripts under .claude/hooks that did little more than call acline
// back: they cost a python3 runtime dependency, a second process on every tool
// call, and made the guard unavailable wherever python3 was not on PATH.
//
// Fail-closed contract for pre-tool-use: any inability to reach a decision (the
// store cannot be opened, the guard panics, hangs past hookTimeout, or the
// `acline` binary is missing) must end in a deny, never an allow. settings.json
// therefore runs it as `acline hook pre-tool-use || exit 2`: a missing binary or
// a crash exits 2, which Claude Code treats as a block, where any other non-zero
// exit would be a non-blocking error and let the tool call through.

// hookTimeout bounds how long the guard may take before the call is denied.
var hookTimeout = 10 * time.Second

// guardRun is the guard; a variable so tests can make it fail.
var guardRun = guardCheckTool

// selfTimeout bounds each acline subprocess a hook starts.
const selfTimeout = 10 * time.Second

// runSelf runs this same binary with args from dir and returns its stdout. The
// hooks call acline back (`dashboard`, `context export`, `note add`) rather than
// re-implementing them, exactly as the Python scripts did.
var runSelf = func(dir string, args ...string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), selfTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%w: %s", err, msg)
		}
		return string(out), err
	}
	return string(out), nil
}

var hookCmd = &cobra.Command{
	Use:   "hook",
	Short: "Claude Code hook entry points (run by .claude/settings.json; not for interactive use)",
}

// hookProjectDir is the project the session belongs to: $CLAUDE_PROJECT_DIR, which
// Claude Code sets for hooks, else the working directory.
func hookProjectDir() string {
	if dir := os.Getenv("CLAUDE_PROJECT_DIR"); dir != "" {
		return dir
	}
	dir, _ := os.Getwd()
	return dir
}

// enterProjectDir moves into the project root so the guard's default vault
// (.claude/vault) and the project marker resolve however far the session's shell
// has wandered.
func enterProjectDir() string {
	dir := hookProjectDir()
	if dir != "" {
		if err := os.Chdir(dir); err == nil {
			guardVault = defaultVaultPath()
		}
	}
	return dir
}

var hookPreToolUseCmd = &cobra.Command{
	Use:   "pre-tool-use",
	Short: "PreToolUse: judge a tool call with the guard (fails closed)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		enterProjectDir()
		return runPreToolUse(os.Stdin, os.Stdout)
	},
}

// runPreToolUse runs the guard on the payload and forwards its decision. Every
// way of not reaching one becomes a deny.
func runPreToolUse(in io.Reader, out io.Writer) error {
	// Read the tunables here, not inside the goroutine: after a timeout that
	// goroutine keeps running, and must not touch state the caller may change.
	guard, timeout := guardRun, hookTimeout
	payload, err := io.ReadAll(in)
	if err != nil {
		return writeHookDeny(out, "blocked: acline could not read the hook payload ("+err.Error()+")")
	}
	type outcome struct {
		out bytes.Buffer
		err error
	}
	done := make(chan *outcome, 1)
	go func() {
		o := &outcome{}
		defer func() {
			if p := recover(); p != nil {
				o.err = fmt.Errorf("guard panicked: %v", p)
			}
			done <- o
		}()
		o.err = guard(bytes.NewReader(payload), &o.out)
	}()
	select {
	case o := <-done:
		if o.out.Len() > 0 {
			_, err := out.Write(o.out.Bytes())
			return err
		}
		if o.err != nil {
			return writeHookDeny(out, "blocked: acline guard could not decide ("+o.err.Error()+")")
		}
		return nil
	case <-time.After(timeout):
		return writeHookDeny(out, fmt.Sprintf("blocked: acline guard did not answer within %s", timeout))
	}
}

func writeHookDeny(out io.Writer, reason string) error {
	return json.NewEncoder(out).Encode(denyOutput(reason))
}

var hookSessionStartCmd = &cobra.Command{
	Use:   "session-start",
	Short: "SessionStart: inject the dashboard and the project's context, and record the running model",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSessionStart(os.Stdin, os.Stdout, hookProjectDir(), os.Getenv("CLAUDE_ENV_FILE"))
	},
}

// runSessionStart prints the SessionStart hook's additionalContext: an unfinished
// first-run BOOTSTRAP.md if there is one, `acline dashboard` and `acline context
// export`. It also records the model that is actually running so every later
// `acline` write carries it. A failing acline never fails the hook; the failure is
// reported in the context instead.
func runSessionStart(in io.Reader, out io.Writer, projectDir, envFile string) error {
	var payload map[string]any
	_ = json.NewDecoder(in).Decode(&payload) // an empty or garbled payload is fine
	exportModel(payload, envFile)

	var sections []string
	if b, err := os.ReadFile(filepath.Join(projectDir, ".claude", "vault", "BOOTSTRAP.md")); err == nil {
		sections = append(sections, "# ONBOARDING IN PROGRESS\n\n"+clip.WithoutFrontMatter(string(b)))
	}
	if dash := strings.TrimSpace(capSection(runAcline(projectDir, "dashboard"), maxDashboardBytes, "acline dashboard")); dash != "" {
		sections = append(sections, "# Dashboard\n\n```\n"+dash+"\n```")
	}
	sections = append(sections, capSection(
		runAcline(projectDir, "context", "export", "--max-rows", strconv.Itoa(sessionContextRows)),
		maxContextBytes, "acline context export"))

	var kept []string
	for _, s := range sections {
		if strings.TrimSpace(s) != "" {
			kept = append(kept, s)
		}
	}
	result := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": strings.Join(kept, "\n\n---\n\n"),
	}}
	return json.NewEncoder(out).Encode(result)
}

// SessionStart context is paid for in every session (and after every /clear),
// so it is bounded instead of growing with the store.
const (
	sessionContextRows = 40       // open-work and capability rows in the context export
	maxDashboardBytes  = 6 << 10  // the dashboard section
	maxContextBytes    = 24 << 10 // the context export section
)

// capSection cuts text to max bytes on a character boundary and says where the
// rest is.
func capSection(text string, max int, fullCommand string) string {
	if len(text) <= max {
		return text
	}
	return clip.Bytes(text, max) + "\n\n…(truncated for the session start; run `" + fullCommand + "` for all of it)\n"
}

func runAcline(dir string, args ...string) string {
	out, err := runSelf(dir, args...)
	if err != nil {
		return fmt.Sprintf("(acline %s failed to run: %v)", strings.Join(args, " "), err)
	}
	return out
}

// exportModel appends `export ACLINE_MODEL=...` to Claude Code's env file, so the
// model that actually ran is recorded by every later `acline` call rather than a
// value hard-coded in CLAUDE.md.
func exportModel(payload map[string]any, envFile string) {
	model := ""
	switch m := payload["model"].(type) {
	case string:
		model = m
	case map[string]any:
		model, _ = m["id"].(string)
	}
	if model == "" || envFile == "" {
		return
	}
	f, err := os.OpenFile(envFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "export ACLINE_MODEL=%s\n", shellQuote(model))
}

var shellSafeRe = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell the way Python's shlex.quote does.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if shellSafeRe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

var hookPreCompactCmd = &cobra.Command{
	Use:   "pre-compact",
	Short: "PreCompact: record MEMORY_LOG lines from the transcript as notes before context is compacted away",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMemoryLogHook(os.Stdin, hookProjectDir())
	},
}

var hookSessionEndCmd = &cobra.Command{
	Use:   "session-end",
	Short: "SessionEnd: record MEMORY_LOG lines not yet captured",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMemoryLogHook(os.Stdin, hookProjectDir())
	},
}

// runMemoryLogHook is shared by PreCompact and SessionEnd, which share per-session
// state so nothing is recorded twice.
func runMemoryLogHook(in io.Reader, projectDir string) error {
	var payload struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
	}
	_ = json.NewDecoder(in).Decode(&payload)
	if payload.SessionID == "" {
		payload.SessionID = "unknown"
	}
	state := filepath.Join(projectDir, ".claude", "vault", ".state", "memory-log-captured.json")
	captureMemoryLog(payload.SessionID, payload.TranscriptPath, state, projectDir)
	return nil
}

var memoryLogRe = regexp.MustCompile(`(?m)^[ \t]*MEMORY_LOG:[ \t]*(.+?)[ \t]*$`)

// extractMemoryLogLines returns the distinct `MEMORY_LOG: ...` lines the agent
// wrote, in order. Only the text blocks of assistant messages are scanned: tool
// results, file contents and injected context (SOUL.md shows the convention with a
// placeholder) never create a note, and the tag must start its own line.
func extractMemoryLogLines(transcriptPath string) []string {
	lines, _ := extractMemoryLogLinesFrom(transcriptPath, 0)
	seen := map[string]bool{}
	var out []string
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}

// extractMemoryLogLinesFrom scans the transcript from byte offset on and returns
// the tagged lines found (in order, possibly repeated) and the offset just past
// the last complete JSONL line, where the next scan resumes. A trailing partial
// line (still being written) is left for the next scan.
func extractMemoryLogLinesFrom(transcriptPath string, offset int64) ([]string, int64) {
	if transcriptPath == "" {
		return nil, offset
	}
	f, err := os.Open(transcriptPath)
	if err != nil {
		return nil, offset
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset
	}
	var lines []string
	end := offset
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		raw, err := r.ReadBytes('\n')
		if err != nil {
			break // EOF: a line without its newline is not complete yet
		}
		end += int64(len(raw))
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		for _, text := range assistantTexts(raw) {
			for _, m := range memoryLogRe.FindAllStringSubmatch(text, -1) {
				lines = append(lines, m[1])
			}
		}
	}
	return lines, end
}

// assistantTexts returns the text of one transcript line if it is an assistant message.
func assistantTexts(raw []byte) []string {
	var entry struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &entry) != nil || entry.Type != "assistant" {
		return nil
	}
	var asString string
	if json.Unmarshal(entry.Message.Content, &asString) == nil {
		return []string{asString}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(entry.Message.Content, &blocks) != nil {
		return nil
	}
	var out []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			out = append(out, b.Text)
		}
	}
	return out
}

// memLogState is how far one session's transcript has been captured: the
// transcript read, the byte offset reached, and short hashes of the lines already
// recorded (a line repeated later, or a transcript read again from the start,
// is not recorded twice). legacy is the line count the old state format kept.
type memLogState struct {
	Path   string   `json:"path,omitempty"`
	Offset int64    `json:"offset"`
	Seen   []string `json:"seen,omitempty"`
	legacy int
}

// captureMemoryLog records each new tagged line as an `acline note` (promoted to a
// decision or memory later, through `acline reflect`). PreCompact, SessionEnd and
// Stop share per-session state, so each turn reads only what was appended since
// the last one instead of the whole transcript again. Capture is best effort: a
// failing or slow `acline note add` must not break the hook.
func captureMemoryLog(sessionID, transcriptPath, statePath, projectDir string) {
	state := loadMemoryLogState(statePath)
	st := state[sessionID]
	if st == nil {
		st = &memLogState{}
		state[sessionID] = st
	}
	if st.Path != transcriptPath || fileSize(transcriptPath) < st.Offset {
		st.Offset = 0 // another or a rewritten transcript: read it again, deduplicated
	}
	st.Path = transcriptPath
	lines, end := extractMemoryLogLinesFrom(transcriptPath, st.Offset)
	seen := map[string]bool{}
	for _, h := range st.Seen {
		seen[h] = true
	}
	for _, line := range lines {
		h := lineHash(line)
		if seen[h] {
			continue
		}
		seen[h] = true
		st.Seen = append(st.Seen, h)
		if st.legacy > 0 { // captured under the old, count-based state
			st.legacy--
			continue
		}
		_, _ = runSelf(projectDir, "note", "add", line, "--source", "conversation")
	}
	st.Offset = end
	saveMemoryLogState(statePath, state)
}

func lineHash(line string) string {
	sum := sha256.Sum256([]byte(line))
	return hex.EncodeToString(sum[:8])
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// loadMemoryLogState reads the state file. The old format kept only a count of
// lines sent per session; such an entry becomes a state that skips that many
// distinct lines from the start of the transcript.
func loadMemoryLogState(path string) map[string]*memLogState {
	state := map[string]*memLogState{}
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return state
	}
	for id, v := range raw {
		var n int
		if json.Unmarshal(v, &n) == nil {
			state[id] = &memLogState{legacy: n}
			continue
		}
		var st memLogState
		if json.Unmarshal(v, &st) == nil {
			state[id] = &st
		}
	}
	return state
}

// saveMemoryLogState writes the state atomically (temp file, then rename).
func saveMemoryLogState(path string, state map[string]*memLogState) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}

func init() {
	hookCmd.AddCommand(hookPreToolUseCmd, hookSessionStartCmd, hookPreCompactCmd, hookSessionEndCmd)
	rootCmd.AddCommand(hookCmd)
}
