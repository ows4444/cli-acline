package orchestrate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"acline/internal/clip"
	"acline/internal/proc"
)

// Agent runs one bounded, non-interactive agent session. It is an interface so
// the orchestrator's logic is tested with a fake, and so another runner can
// replace Claude Code without touching the rules in this package.
type Agent interface {
	Run(ctx context.Context, req AgentRequest) AgentResult
}

// AgentRequest is everything an agent run needs. Env is the complete
// environment; the orchestrator builds it (see agentEnv) so the agent runs with
// agent identity and never inherits the approval token.
type AgentRequest struct {
	Prompt       string
	Dir          string
	Env          []string
	AllowTools   []string
	DenyTools    []string
	MaxBudgetUSD float64
	Timeout      time.Duration
	// Settings is extra Claude Code settings JSON (`--settings`): the Bash
	// sandbox, unless the person launching turned it off (see sandboxSettings).
	Settings string
}

// AgentResult is what came back. CostUSD is best effort: it is read from the
// agent's own report, never used to decide what happens next except for the
// spend limit, which the agent is also given directly as a hard cap.
type AgentResult struct {
	ExitCode int
	TimedOut bool
	Output   string
	CostUSD  float64
	StartErr error // the agent could not be started at all

	// Best-effort diagnostics read from the agent's own JSON report. They are
	// shown to the person supervising and logged; they never decide what the
	// orchestrator does next.
	Summary string   // what the agent said it did (truncated)
	Denied  []string // tool calls the permission mode refused
	Turns   int
}

// maxOutput bounds how much agent output is held in memory.
const maxOutput = 1 << 20

// ClaudeAgent runs Claude Code headless (`claude -p`).
type ClaudeAgent struct {
	Bin string // default "claude"
}

// Args builds the claude command line. Exposed so a dry run can show exactly
// what would be launched.
//
// The safety-relevant choices, all verified against `claude --help` (2.1.x):
//   - dontAsk + --permission-prompts none: anything not on the allow list is
//     denied, nothing waits on a person, and bypassPermissions is never used.
//   - --max-budget-usd: a spend cap the agent itself enforces.
//   - --no-session-persistence: a step leaves no resumable session behind.
//   - --settings: the Bash sandbox (sandbox.go), unless turned off.
//
// The installed version has no turn limit, so the timeout is the only bound on
// how long a step runs. The prompt goes on stdin, not argv.
func (a ClaudeAgent) Args(req AgentRequest) []string {
	args := []string{
		"-p", "--output-format", "json",
		"--permission-mode", "dontAsk", "--permission-prompts", "none",
		"--no-session-persistence",
	}
	if req.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", req.MaxBudgetUSD))
	}
	if len(req.AllowTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(req.AllowTools, ","))
	}
	if len(req.DenyTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(req.DenyTools, ","))
	}
	if req.Settings != "" {
		args = append(args, "--settings", req.Settings)
	}
	return args
}

func (a ClaudeAgent) bin() string {
	if a.Bin == "" {
		return "claude"
	}
	return a.Bin
}

// Run launches claude and waits for it, killing its whole process group on
// timeout or cancellation.
func (a ClaudeAgent) Run(ctx context.Context, req AgentRequest) AgentResult {
	bin, err := exec.LookPath(a.bin())
	if err != nil {
		return AgentResult{ExitCode: -1, StartErr: fmt.Errorf("%s is not installed or not on PATH", a.bin())}
	}
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, a.Args(req)...)
	cmd.Dir = req.Dir
	cmd.Env = req.Env
	cmd.Stdin = strings.NewReader(req.Prompt)
	out := &cappedBuffer{max: maxOutput}
	cmd.Stdout, cmd.Stderr = out, out
	proc.Bound(cmd)

	runErr := cmd.Run()
	res := AgentResult{Output: out.String()}
	res.CostUSD, res.Summary, res.Denied, res.Turns = parseReport(res.Output)
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.TimedOut, res.ExitCode = true, -1
	case errors.Is(ctx.Err(), context.Canceled):
		res.ExitCode = -1
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		res.ExitCode, res.StartErr = -1, runErr
	}
	return res
}

// maxSummary bounds how much of the agent's own account is kept.
const maxSummary = 600

// parseReport reads claude's single-result JSON, tolerating any field being
// absent or shaped differently: it returns zero values rather than failing.
func parseReport(output string) (cost float64, summary string, denied []string, turns int) {
	var r struct {
		Cost    float64           `json:"total_cost_usd"`
		Result  string            `json:"result"`
		Turns   int               `json:"num_turns"`
		Denials []json.RawMessage `json:"permission_denials"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &r); err != nil {
		return 0, "", nil, 0
	}
	summary = strings.TrimSpace(r.Result)
	if len(summary) > maxSummary {
		summary = clip.Bytes(summary, maxSummary) + "…"
	}
	seen := map[string]bool{}
	for _, raw := range r.Denials {
		var d struct {
			Tool  string          `json:"tool_name"`
			Input json.RawMessage `json:"tool_input"`
		}
		label := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &d) == nil && d.Tool != "" {
			label = d.Tool
			if len(d.Input) > 0 {
				label += " " + string(d.Input)
			}
		}
		if len(label) > 160 {
			label = clip.Bytes(label, 160) + "…"
		}
		if !seen[label] && len(denied) < 10 {
			seen[label] = true
			denied = append(denied, label)
		}
	}
	return r.Cost, summary, denied, r.Turns
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil // never fail the child on a full buffer
}

func (c *cappedBuffer) String() string { return c.buf.String() }
