package orchestrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fakeClaude writes an executable that records how it was called and prints a
// claude-style JSON result.
func fakeClaude(t *testing.T, body string) (bin, dump string) {
	t.Helper()
	dir := t.TempDir()
	dump = filepath.Join(dir, "dump")
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"{ echo \"ARGS: $*\"; echo \"PWD: $(pwd)\"; echo \"STDIN: $(cat)\"; env; } > " + dump + "\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, dump
}

func TestClaudeAgentPassesTheSafeFlagsPromptAndEnvironment(t *testing.T) {
	bin, dump := fakeClaude(t, "echo '{\"type\":\"result\",\"total_cost_usd\":0.12}'\n")
	work := t.TempDir()
	req := AgentRequest{
		Prompt: "the whole brief", Dir: work, MaxBudgetUSD: 0.5, Timeout: 10 * time.Second,
		Env:        []string{"PATH=" + os.Getenv("PATH"), "ACLINE_ACTOR_TYPE=agent"},
		AllowTools: []string{"Read", "Bash(go test *)"}, DenyTools: []string{"Bash(acline approve*)"},
	}
	res := ClaudeAgent{Bin: bin}.Run(context.Background(), req)
	if res.ExitCode != 0 || res.StartErr != nil || res.TimedOut {
		t.Fatalf("result = %+v", res)
	}
	if res.CostUSD != 0.12 {
		t.Errorf("cost = %v, want 0.12", res.CostUSD)
	}
	b, _ := os.ReadFile(dump)
	got := string(b)
	for _, want := range []string{
		"-p", "--output-format json", "--permission-mode dontAsk", "--permission-prompts none",
		"--no-session-persistence", "--max-budget-usd 0.50",
		"--allowedTools Read,Bash(go test *)", "--disallowedTools Bash(acline approve*)",
		"STDIN: the whole brief", "ACLINE_ACTOR_TYPE=agent",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "bypassPermissions") || strings.Contains(got, "ACLINE_APPROVAL_TOKEN") {
		t.Errorf("unsafe flag or leaked token:\n%s", got)
	}
	// os.Getwd may report a symlink-resolved path (/private/var on macOS).
	if !strings.Contains(got, "PWD: ") || !strings.HasSuffix(strings.TrimSpace(strings.SplitN(strings.SplitN(got, "PWD: ", 2)[1], "\n", 2)[0]), filepath.Base(work)) {
		t.Errorf("did not run in the requested directory:\n%s", got)
	}
}

func TestClaudeAgentTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "child-survived")
	// The child outlives its parent unless the group is killed.
	bin, _ := fakeClaude(t, "(sleep 2; touch "+marker+") &\nsleep 30\n")
	start := time.Now()
	res := ClaudeAgent{Bin: bin}.Run(context.Background(), AgentRequest{Env: os.Environ(), Timeout: 300 * time.Millisecond})
	if !res.TimedOut {
		t.Fatalf("result = %+v", res)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s to give up", time.Since(start))
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a child process survived the timeout")
	}
}

func TestClaudeAgentReportsAMissingBinaryAndCancellation(t *testing.T) {
	if res := (ClaudeAgent{Bin: "no-such-claude-binary"}).Run(context.Background(), AgentRequest{}); res.StartErr == nil {
		t.Errorf("missing binary: %+v", res)
	}
	bin, _ := fakeClaude(t, "sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	res := ClaudeAgent{Bin: bin}.Run(ctx, AgentRequest{Env: os.Environ()})
	if res.TimedOut || res.ExitCode == 0 {
		t.Errorf("cancelled run = %+v", res)
	}
}

func TestOutputIsCappedButTheChildIsNotBlocked(t *testing.T) {
	bin, _ := fakeClaude(t, "head -c 3000000 /dev/zero | tr '\\0' 'x'\n")
	res := ClaudeAgent{Bin: bin}.Run(context.Background(), AgentRequest{Env: os.Environ(), Timeout: 20 * time.Second})
	if res.ExitCode != 0 || len(res.Output) > maxOutput {
		t.Errorf("exit=%d len=%d", res.ExitCode, len(res.Output))
	}
}

func TestParseReportKeepsAMultibyteSummaryValid(t *testing.T) {
	out := `{"result":"` + strings.Repeat("日", 400) + `","total_cost_usd":0.1}`
	_, summary, _, _ := parseReport(out)
	if !utf8.ValidString(summary) || !strings.HasSuffix(summary, "…") {
		t.Fatalf("summary = %q", summary)
	}
}
