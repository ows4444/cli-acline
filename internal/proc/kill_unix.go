//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

// killGroup runs cmd in its own process group and, on cancel or timeout, kills
// the whole group so what it spawned does not outlive it.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
