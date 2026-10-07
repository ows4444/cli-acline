//go:build !windows

package proc

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// parentWithGrandchild starts a shell that starts a long sleep, prints the
// sleep's pid and waits: a tool with a child of its own, as `go test` has its
// test binaries. It runs until the timeout and returns the grandchild's pid.
func parentWithGrandchild(t *testing.T, prepare func(*exec.Cmd)) (pid int, took time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "sleep 60 & echo $!; wait")
	var out bytes.Buffer
	cmd.Stdout = &out // the grandchild inherits it, as a tool's children do
	prepare(cmd)
	start := time.Now()
	err := cmd.Run()
	took = time.Since(start)
	if err == nil {
		t.Fatal("the command outlived its timeout")
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(out.String()))
	if convErr != nil {
		t.Fatalf("no grandchild pid in %q (run: %v)", out.String(), err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid, took
}

// gone reports whether the process has exited, giving the kernel a moment.
func gone(pid int) bool {
	for i := 0; i < 100; i++ {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestBoundKillsWhatTheChildSpawnedOnTimeout(t *testing.T) {
	pid, took := parentWithGrandchild(t, Bound)
	if !gone(pid) {
		t.Fatalf("grandchild %d outlived the timeout", pid)
	}
	if took >= WaitDelay {
		t.Fatalf("Run took %s: it waited out WaitDelay instead of the group dying", took)
	}
}

// Without Bound only the direct child is killed: this is what Bound is for,
// and what the test above would not notice if the shell cleaned up by itself.
func TestWithoutBoundTheGrandchildSurvives(t *testing.T) {
	pid, _ := parentWithGrandchild(t, func(cmd *exec.Cmd) { cmd.WaitDelay = 100 * time.Millisecond })
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("grandchild %d is gone without Bound (%v): the other test proves nothing", pid, err)
	}
}

func TestBoundSetsTheWaitDelayAndItsOwnGroup(t *testing.T) {
	cmd := exec.Command("true")
	Bound(cmd)
	if cmd.WaitDelay != WaitDelay {
		t.Errorf("WaitDelay = %s, want %s", cmd.WaitDelay, WaitDelay)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Error("the command does not get its own process group")
	}
	if err := cmd.Cancel(); err != nil { // not started: nothing to kill
		t.Errorf("Cancel before start: %v", err)
	}
}
