// Package checkrun runs a real verification tool and turns its outcome into
// a check result, so sast/sca/lint/test results come from a tool's exit code
// rather than from an agent's say-so.
//
// The rules that keep a result honest:
//   - "pass" only ever means the tool ran and exited 0.
//   - A tool that is not installed (or a project with no default runner)
//     yields "skipped", never "pass".
//   - Commands run directly, never through a shell (see SplitCommand).
package checkrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"acline/internal/clip"
	"acline/internal/proc"
)

// DefaultTimeout bounds one tool run.
const DefaultTimeout = 5 * time.Minute

// maxOutput is how much of the tool's output tail is kept in the detail.
const maxOutput = 1500

// Kinds lists the check kinds that have a runner.
var Kinds = []string{"test", "sast", "sca", "lint"}

// defaults are the Go-project runners, used when the working directory holds a
// go.mod. Other ecosystems pass an explicit command.
var defaults = map[string]string{
	"test": "go test ./...",
	"lint": "go vet ./...",
	"sast": "gosec ./...",
	"sca":  "govulncheck ./...",
}

// Result is the outcome of one run, ready for Store.AddCheck.
type Result struct {
	Status string // pass | fail | skipped
	Detail string
}

// Run executes the runner for kind in dir. A non-empty command overrides the
// default; it is split into words by SplitCommand and executed without a shell.
func Run(ctx context.Context, kind, dir, command string, timeout time.Duration) (Result, error) {
	known := false
	for _, k := range Kinds {
		known = known || k == kind
	}
	if !known {
		return Result{}, fmt.Errorf("no runner for check kind %q (runnable: %s)", kind, strings.Join(Kinds, ", "))
	}
	if strings.TrimSpace(command) == "" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
			return Result{"skipped", fmt.Sprintf("no default %s runner for this project (no go.mod); pass an explicit command", kind)}, nil
		}
		command = defaults[kind]
	}
	argv, err := SplitCommand(command)
	if err != nil {
		return Result{}, err
	}
	if len(argv) == 0 {
		return Result{}, fmt.Errorf("empty %s runner command", kind)
	}
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return Result{"skipped", argv[0] + " is not installed; nothing was verified"}, nil
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	cmd.Dir = dir
	out := &tailBuffer{max: maxBuffered}
	cmd.Stdout, cmd.Stderr = out, out
	proc.Bound(cmd) // kill what the tool spawned, and stop waiting for its pipes
	runErr := cmd.Run()

	detail := "ran: " + strings.Join(argv, " ")
	if tail := tailOf(out.String()); tail != "" {
		detail += "\n" + tail
	}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		return Result{"pass", detail}, nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return Result{"fail", fmt.Sprintf("timed out after %s; %s", timeout, detail)}, nil
	case errors.Is(ctx.Err(), context.Canceled):
		// The caller gave up (an MCP client timed out, a person pressed ^C): the
		// tool did not fail, and nothing should be recorded.
		return Result{}, ErrCanceled
	case errors.As(runErr, &exitErr):
		return Result{"fail", fmt.Sprintf("exit %d; %s", exitErr.ExitCode(), detail)}, nil
	default:
		return Result{"skipped", fmt.Sprintf("could not start %s: %v", argv[0], runErr)}, nil
	}
}

// SplitCommand splits a runner command into argv the way a POSIX shell splits
// words, without being one: whitespace separates, '...' is literal, "..." and a
// backslash escape the next character, and nothing is expanded or interpreted
// (`;`, `|`, `$HOME` stay literal). An unterminated quote or trailing backslash
// is an error rather than a guess.
func SplitCommand(command string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inWord := false
	for i := 0; i < len(command); i++ {
		c := command[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				args = append(args, cur.String())
				cur.Reset()
				inWord = false
			}
		case c == '\\':
			if i+1 >= len(command) {
				return nil, fmt.Errorf("runner command %q ends with a backslash", command)
			}
			i++
			cur.WriteByte(command[i])
			inWord = true
		case c == '\'':
			end := strings.IndexByte(command[i+1:], '\'')
			if end < 0 {
				return nil, fmt.Errorf("runner command %q has an unterminated ' quote", command)
			}
			cur.WriteString(command[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			i++
			for ; i < len(command) && command[i] != '"'; i++ {
				if command[i] == '\\' && i+1 < len(command) && (command[i+1] == '"' || command[i+1] == '\\') {
					i++
				}
				cur.WriteByte(command[i])
			}
			if i >= len(command) {
				return nil, fmt.Errorf("runner command %q has an unterminated \" quote", command)
			}
			inWord = true
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		args = append(args, cur.String())
	}
	return args, nil
}

// ErrCanceled is returned when the caller's context is cancelled before the
// tool finishes. It is not a result: the caller records nothing.
var ErrCanceled = errors.New("check run cancelled before the tool finished; nothing was recorded")

// maxBuffered is how much of the tool's output is held while it runs (the end
// is what matters: failures and summaries are printed last).
const maxBuffered = 64 << 10

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxOutput {
		s = "..." + clip.Tail(s, maxOutput)
	}
	return s
}
