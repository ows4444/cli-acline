// Package proc holds the process handling shared by everything acline runs as a
// child: the orchestrator's agent and `check run`'s tools. Both may spawn
// grandchildren (claude's tools, `go test`'s test binaries) that must not
// outlive a timeout.
package proc

import (
	"os/exec"
	"time"
)

// WaitDelay bounds how long Wait lingers for the output pipes after the process
// is killed. Without it a grandchild that inherited stdout keeps Wait blocked
// past the timeout.
const WaitDelay = 5 * time.Second

// Bound makes cmd killable as a whole: its process group is killed on cancel
// or timeout (Unix; on Windows only the direct child), and Wait gives up on the
// output pipes WaitDelay after that.
func Bound(cmd *exec.Cmd) {
	killGroup(cmd)
	cmd.WaitDelay = WaitDelay
}
