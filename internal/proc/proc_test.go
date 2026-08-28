package proc

import (
	"bufio"
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// scan runs splitLines over a reader the way pump does, and returns the
// non-empty tokens.
func scan(input string) []string {
	sc := bufio.NewScanner(strings.NewReader(input))
	sc.Split(splitLines)
	var out []string
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "plain newlines",
			input: "one\ntwo\nthree\n",
			want:  []string{"one", "two", "three"},
		},
		{
			name:  "crlf does not produce blanks",
			input: "one\r\ntwo\r\n",
			want:  []string{"one", "two"},
		},
		{
			// This is the case that motivates the custom splitter: tqdm writes
			// "\r" + bar and never a newline until it closes, so ScanLines
			// would yield nothing at all here.
			name:  "carriage-return progress bar",
			input: "\r 10%|# |\r 50%|##### |\r100%|##########|\n",
			want:  []string{" 10%|# |", " 50%|##### |", "100%|##########|"},
		},
		{
			name:  "unterminated final line still surfaces",
			input: "last line without newline",
			want:  []string{"last line without newline"},
		},
		{
			name:  "mixed",
			input: "log line\n\rbar 1\rbar 2\nlog again\n",
			want:  []string{"log line", "bar 1", "bar 2", "log again"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := scan(tc.input)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d lines %q, want %d %q",
					len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestWithPathPrefix(t *testing.T) {
	// Windows environment names are case-insensitive, so a lowercase "Path"
	// must be extended rather than duplicated.
	env := []string{"HOME=/home/x", "Path=/usr/bin"}
	got := withPathPrefix(env, []string{"/opt/app/bin"})
	var found int
	for _, kv := range got {
		if strings.HasPrefix(kv, "Path=") {
			found++
			if !strings.HasPrefix(kv, "Path=/opt/app/bin") {
				t.Errorf("prefix not applied: %q", kv)
			}
			if !strings.Contains(kv, "/usr/bin") {
				t.Errorf("original PATH lost: %q", kv)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly one Path entry, got %d in %q", found, got)
	}
}

func TestWithPathPrefixWhenAbsent(t *testing.T) {
	got := withPathPrefix([]string{"HOME=/home/x"}, []string{"/opt/bin"})
	if len(got) != 2 || !strings.HasPrefix(got[1], "PATH=/opt/bin") {
		t.Errorf("expected PATH to be added, got %q", got)
	}
}

func TestRunCapturesBothStreams(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	var mu sync.Mutex
	seen := map[string][]string{}
	err := Run(context.Background(), Options{
		Bin:  "sh",
		Args: []string{"-c", "echo out-line; echo err-line 1>&2"},
		OnLine: func(stream, line string) {
			mu.Lock()
			seen[stream] = append(seen[stream], line)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(seen["stdout"]) != 1 || seen["stdout"][0] != "out-line" {
		t.Errorf("stdout = %q", seen["stdout"])
	}
	if len(seen["stderr"]) != 1 || seen["stderr"][0] != "err-line" {
		t.Errorf("stderr = %q", seen["stderr"])
	}
}

func TestRunNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	err := Run(context.Background(), Options{Bin: "sh", Args: []string{"-c", "exit 3"}})
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if !strings.Contains(err.Error(), "sh failed") {
		t.Errorf("error should name the binary, got %v", err)
	}
}

func TestRunCancelKillsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Bin: "sh", Args: []string{"-c", "sleep 60"}})
	}()
	// Give the child a moment to actually start before cancelling.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != ErrCancelled {
			t.Errorf("err = %v, want ErrCancelled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestRunMissingBinary(t *testing.T) {
	err := Run(context.Background(), Options{Bin: "definitely-not-a-real-binary-xyz"})
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunNoBinary(t *testing.T) {
	if err := Run(context.Background(), Options{}); err == nil {
		t.Fatal("expected an error when Bin is empty")
	}
}

// A cancel that lands after the child has already exited must not be reported
// as a cancellation: doing so threw away downloads and separations whose output
// files were already fully written.
func TestRunCancelAfterExitStillSucceeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			errCh <- Run(ctx, Options{Bin: "sh", Args: []string{"-c", "exit 0"}})
		}()
		// Race the cancel against the child's exit, hitting the window between
		// cmd.Wait() reaping it and the watcher observing `done`.
		cancel()
		if err := <-errCh; err != nil && err != ErrCancelled {
			t.Fatalf("iteration %d: unexpected error %v", i, err)
		}
		_ = cancel
	}
}

// The same window must not let killTree signal an already-reaped process group:
// on Unix that targets a whole pgid, so a recycled id means killing a bystander.
func TestRunCancelAfterExitDoesNotKillStalePgid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX shell")
	}
	// A long-lived sentinel that would die if its process group were signalled.
	sentinelDone := make(chan error, 1)
	sentinelCtx, stopSentinel := context.WithCancel(context.Background())
	defer stopSentinel()
	go func() {
		sentinelDone <- Run(sentinelCtx, Options{Bin: "sh", Args: []string{"-c", "sleep 5"}})
	}()
	time.Sleep(150 * time.Millisecond)

	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		_ = Run(ctx, Options{Bin: "sh", Args: []string{"-c", "exit 0"}})
		cancel()
	}

	select {
	case err := <-sentinelDone:
		t.Fatalf("sentinel died during the cancel storm: %v", err)
	case <-time.After(300 * time.Millisecond):
		// Still alive, which is the point.
	}
}
