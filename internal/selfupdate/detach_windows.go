//go:build windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

// detachedProcess keeps the relaunched app alive after this process exits,
// instead of being torn down with the console group it was started from.
const detachedProcess = 0x00000008

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
}
