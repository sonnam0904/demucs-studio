//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

// configureChild puts the child in its own process group so killTree can signal
// the group and reach demucs' worker processes.
func configureChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	// Negative pid targets the whole process group.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
