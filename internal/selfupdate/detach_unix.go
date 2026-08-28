//go:build !windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

// detach puts the relaunched app in its own session, so it does not die with
// the process that spawned it and does not inherit its controlling terminal.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
