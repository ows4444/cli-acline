//go:build !windows

package checkrun

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// script writes an executable shell script, since Run never uses a shell.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// A grandchild holding the output pipe (as `go test`'s test binaries do)
// kept Run blocked long after its timeout.
func TestRunReturnsAtItsTimeoutEvenWhenAGrandchildHoldsTheOutput(t *testing.T) {
	tool := script(t, "sleep 30 &\nsleep 30")
	start := time.Now()
	r, err := Run(context.Background(), "test", t.TempDir(), tool, 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Fatalf("Run took %s after a 300ms timeout", took)
	}
	if r.Status != "fail" || !strings.Contains(r.Detail, "timed out") {
		t.Fatalf("result = %+v", r)
	}
}

// A caller cancelling (an MCP client that gave up) is not the tool failing.
func TestACancelledRunIsNotRecordedAsAFailure(t *testing.T) {
	tool := script(t, "sleep 30")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	r, err := Run(ctx, "test", t.TempDir(), tool, time.Minute)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("cancelled run = %+v, %v; want ErrCanceled", r, err)
	}
}

// Output is bounded in memory, and the kept tail is valid UTF-8.
func TestOutputIsBoundedAndCutOnACharacterBoundary(t *testing.T) {
	tool := script(t, `i=0; while [ $i -lt 20000 ]; do printf 'ééééééééé\n'; i=$((i+1)); done`)
	r, err := Run(context.Background(), "test", t.TempDir(), tool, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(r.Detail) {
		t.Fatal("detail is not valid UTF-8")
	}
	if len(r.Detail) > 4*maxOutput {
		t.Fatalf("detail is %d bytes", len(r.Detail))
	}
}
