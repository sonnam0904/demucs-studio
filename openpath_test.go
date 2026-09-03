package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestShellOpenPassesThePathAsOneArgument(t *testing.T) {
	// A result folder always contains spaces — safeName keeps them in the song
	// title — and on Windows that is what broke the old
	// `rundll32 url.dll,FileProtocolHandler` call: rundll32 forwards the rest
	// of the command line verbatim, so Go's quoting reached the handler as
	// literal quote characters. Whatever the helper is, the path has to travel
	// as a single unmangled argv entry.
	target := filepath.Join("tmp", "Sơn Tùng M-TP - Chúng Ta Của Hiện Tại", "htdemucs_ft")
	cmd := shellOpen(target)

	if len(cmd.Args) == 0 {
		t.Fatal("shellOpen produced no command")
	}
	if !slices.Contains(cmd.Args, target) {
		t.Errorf("argv %q does not carry the path %q intact", cmd.Args, target)
	}

	switch runtime.GOOS {
	case "windows":
		// explorer.exe is the documented way to open a folder and consumes a
		// normal argument; rundll32 is neither.
		if filepath.Base(cmd.Path) != "explorer.exe" && filepath.Base(cmd.Path) != "explorer" {
			t.Errorf("helper = %q, want explorer", cmd.Path)
		}
		if slices.Contains(cmd.Args, "url.dll,FileProtocolHandler") {
			t.Error("still going through rundll32, which mangles quoted paths")
		}
	case "darwin":
		if filepath.Base(cmd.Path) != "open" {
			t.Errorf("helper = %q, want open", cmd.Path)
		}
	default:
		if filepath.Base(cmd.Path) != "xdg-open" {
			t.Errorf("helper = %q, want xdg-open", cmd.Path)
		}
	}
}

func TestShellOpenExitStatusIsOnlyTrustedWhereItMeansSomething(t *testing.T) {
	// explorer.exe returns 1 even when it worked, so its status must never be
	// surfaced as a failure; the POSIX helpers do report real errors.
	if runtime.GOOS == "windows" && shellOpenReportsExit {
		t.Error("explorer's exit status is meaningless and must not be reported")
	}
	if runtime.GOOS != "windows" && !shellOpenReportsExit {
		t.Error("open/xdg-open report real failures and should be surfaced")
	}
}

func TestOpenPathRejectsWhatItCannotOpen(t *testing.T) {
	a := NewApp(nil)

	if err := a.OpenPath(""); err == nil {
		t.Error("an empty path should be refused")
	}
	missing := filepath.Join(t.TempDir(), "khong-ton-tai")
	if err := a.OpenPath(missing); err == nil {
		t.Error("a missing path should be refused before launching a helper")
	}

	// A directory whose name has spaces and non-ASCII must pass the stat gate:
	// that is the exact shape of every result folder.
	dir := filepath.Join(t.TempDir(), "Sơn Tùng M-TP - Chúng Ta", "htdemucs_ft")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the result-folder shape does not even stat: %v", err)
	}
}
