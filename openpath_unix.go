//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

// shellOpen builds the command that hands target to the desktop, for either a
// directory or a file.
func shellOpen(target string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", target)
	}
	return exec.Command("xdg-open", target)
}

// shellOpenReportsExit is true here: both `open` and `xdg-open` exit non-zero
// when they genuinely failed, so a bad status is worth telling the user about.
const shellOpenReportsExit = true
