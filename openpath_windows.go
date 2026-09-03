//go:build windows

package main

import "os/exec"

// shellOpen builds the command that hands target to the Windows shell, for
// either a directory or a file.
//
// explorer.exe, not `rundll32 url.dll,FileProtocolHandler` which this used
// before. Two problems with that one:
//
//   - rundll32 passes the remainder of the command line to the entry point
//     verbatim, quotes and all. Go quotes any argument containing a space, and
//     every result folder has one — safeName keeps the spaces in a song title —
//     so FileProtocolHandler received a quoted string and found nothing.
//   - it is not the documented way to open a folder in the first place; it is
//     a URL handler that happens to accept some paths.
//
// explorer.exe takes a normal argument and opens a directory in a window or a
// file with its registered application. Its exit status is famously 1 even on
// success, so callers must not read anything into it.
func shellOpen(target string) *exec.Cmd {
	return exec.Command("explorer", target)
}

// shellOpenReportsExit is false because explorer.exe returns 1 on success;
// logging its status would cry wolf on every single open.
const shellOpenReportsExit = false
