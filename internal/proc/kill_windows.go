//go:build windows

package proc

import "os/exec"

// killGroup is a no-op here: exec.CommandContext's default kill of the direct
// child applies, without process-group cleanup.
func killGroup(cmd *exec.Cmd) {}
