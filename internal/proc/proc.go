// Package proc runs the external CLIs the app drives (yt-dlp, ffmpeg, demucs,
// audio-separator) while streaming their output line by line.
//
// Two details matter for this app in particular:
//
//   - Python progress bars (tqdm) redraw with a bare carriage return and never
//     emit a newline until the bar finishes, so a plain bufio.ScanLines reader
//     shows nothing for minutes. splitLines below treats \r as a terminator.
//   - Cancelling a separation must kill the whole tree: demucs spawns worker
//     processes, and killing only the parent leaves them burning CPU.
package proc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Options describes one external command invocation.
type Options struct {
	Bin  string
	Args []string
	Dir  string

	// ExtraEnv entries ("KEY=value") are appended to the parent environment.
	ExtraEnv []string
	// PrependPath entries are prefixed to PATH, so a bundled ffmpeg wins over
	// whatever the system has.
	PrependPath []string

	// OnLine receives every output line. stream is "stdout" or "stderr".
	// Called from reader goroutines; implementations must be safe to call
	// concurrently.
	OnLine func(stream, line string)
}

// ErrCancelled reports that the context was cancelled before the child exited.
var ErrCancelled = errors.New("cancelled")

// Run starts the command and blocks until it exits or ctx is cancelled.
func Run(ctx context.Context, o Options) error {
	if o.Bin == "" {
		return errors.New("proc: no executable given")
	}
	cmd := exec.Command(o.Bin, o.Args...)
	cmd.Dir = o.Dir
	cmd.Env = buildEnv(o)
	configureChild(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", filepath.Base(o.Bin), err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); pump(stdout, "stdout", o.OnLine) }()
	go func() { defer wg.Done(); pump(stderr, "stderr", o.OnLine) }()

	// Watch for cancellation while the child runs.
	//
	// The `reaped` flag is load-bearing, not bookkeeping. Without it there are
	// two bugs. First, cancelling in the window between cmd.Wait() reaping the
	// child and this goroutine observing `done` would signal a dead PID — and
	// because killTree targets a process *group* on Unix, a recycled pgid means
	// killing something unrelated. Second, deciding cancellation from ctx.Err()
	// alone would report ErrCancelled for a command that had already succeeded,
	// throwing away a finished download or a 30-minute separation.
	//
	// So: only kill while the process is known live, and only report
	// cancellation when we actually killed it.
	done := make(chan struct{})
	var mu sync.Mutex
	var killed, reaped bool
	go func() {
		select {
		case <-ctx.Done():
			mu.Lock()
			if !reaped {
				killed = true
				killTree(cmd)
			}
			mu.Unlock()
		case <-done:
		}
	}()

	wg.Wait()
	waitErr := cmd.Wait()

	mu.Lock()
	reaped = true
	wasKilled := killed
	mu.Unlock()
	close(done)

	if wasKilled {
		return ErrCancelled
	}
	if waitErr != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(o.Bin), waitErr)
	}
	return nil
}

// Output runs the command and returns its combined stdout, additionally
// forwarding lines to OnLine when set. Useful for the `--list_models` and
// `--version` style probes.
func Output(ctx context.Context, o Options) (string, error) {
	var buf bytes.Buffer
	var mu sync.Mutex
	forward := o.OnLine
	o.OnLine = func(stream, line string) {
		if stream == "stdout" {
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
		}
		if forward != nil {
			forward(stream, line)
		}
	}
	err := Run(ctx, o)
	return buf.String(), err
}

// Probe runs a short command and reports whether it succeeded, returning the
// first non-empty output line (typically a version string).
func Probe(ctx context.Context, bin string, args ...string) (string, bool) {
	out, err := Output(ctx, Options{Bin: bin, Args: args})
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line, true
		}
	}
	return "", true
}

func buildEnv(o Options) []string {
	env := os.Environ()
	if len(o.PrependPath) > 0 {
		env = withPathPrefix(env, o.PrependPath)
	}
	return append(env, o.ExtraEnv...)
}

func withPathPrefix(env []string, prefix []string) []string {
	const key = "PATH"
	joined := strings.Join(prefix, string(os.PathListSeparator))
	for i, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		// Windows environment variable names are case-insensitive.
		if strings.EqualFold(name, key) {
			env[i] = name + "=" + joined + string(os.PathListSeparator) + value
			return env
		}
	}
	return append(env, key+"="+joined)
}

func pump(r io.Reader, stream string, onLine func(string, string)) {
	if onLine == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(splitLines)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if line == "" {
			continue
		}
		onLine(stream, line)
	}
}

// splitLines is a bufio.SplitFunc that terminates a token on \n, \r or \r\n, so
// carriage-return progress bars surface immediately.
func splitLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		// Swallow the \n of a \r\n pair so it does not yield an empty token.
		width := 1
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			width = 2
		} else if data[i] == '\r' && i+1 == len(data) && !atEOF {
			// Cannot yet tell whether a \n follows; wait for more bytes.
			return 0, nil, nil
		}
		return i + width, data[:i], nil
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
