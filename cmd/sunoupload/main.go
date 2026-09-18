// Command sunoupload is a throwaway harness to validate the Suno upload client
// against the real API using a hand-pasted Bearer token. It is not part of the
// app — once the upload flow is confirmed and the in-app token acquisition is
// built, this can be deleted.
//
// Usage:
//
//	go run ./cmd/sunoupload -token "<JWT>" -file "/path/to/audio.wav"
//
// Get -token from a logged-in suno.com session (DevTools → any studio-api
// request → Authorization header, the part after "Bearer "). It lives ~1 hour.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"demucs-studio/internal/suno"
)

// stdoutReporter satisfies suno.Reporter by printing to the terminal.
type stdoutReporter struct{}

func (stdoutReporter) Log(level, text string) { fmt.Printf("[%s] %s\n", level, text) }

func (stdoutReporter) Logf(level, format string, args ...any) {
	fmt.Printf("[%s] %s\n", level, fmt.Sprintf(format, args...))
}

func (stdoutReporter) Step(phase string, fraction float64, label, detail string) {
	pct := ""
	if fraction >= 0 {
		pct = fmt.Sprintf(" %3.0f%%", fraction*100)
	}
	fmt.Printf("→ [%s]%s %s %s\n", phase, pct, label, detail)
}

func main() {
	token := flag.String("token", "", "Bearer JWT from a logged-in suno.com session")
	tokenFile := flag.String("token-file", "", "path to a file holding the Bearer JWT (avoids shell truncation)")
	file := flag.String("file", "", "path to the audio file to upload (required)")
	device := flag.String("device", "", "device-id header (optional; a random UUID is used if empty)")
	flag.Parse()

	// Token can come from -token-file, -token, or $SUNO_TOKEN. The file form is
	// preferred: the JWT is ~1000 chars and pasting it on a command line both
	// truncates easily and leaks into shell history.
	raw := *token
	switch {
	case strings.TrimSpace(*tokenFile) != "":
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "không đọc được token-file: %v\n", err)
			os.Exit(2)
		}
		raw = string(b)
	case strings.TrimSpace(raw) == "":
		raw = os.Getenv("SUNO_TOKEN")
	}

	if strings.TrimSpace(raw) == "" || strings.TrimSpace(*file) == "" {
		fmt.Fprintln(os.Stderr, "cần token (-token / -token-file / $SUNO_TOKEN) và -file. Xem: go run ./cmd/sunoupload -h")
		os.Exit(2)
	}
	// Strip a pasted "Bearer " prefix and any surrounding whitespace/newlines so
	// either form, and a file with a trailing newline, both work.
	tok := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "Bearer "))
	if strings.ContainsAny(tok, " \n\t…") {
		fmt.Fprintln(os.Stderr, "⚠ token chứa khoảng trắng/xuống dòng/dấu … — có thể đã bị cắt khi copy. Hãy copy lại đầy đủ.")
	}

	dev := strings.TrimSpace(*device)
	if dev == "" {
		dev = randomUUID()
		fmt.Printf("device-id (tự sinh): %s\n", dev)
	}

	// Ctrl-C cancels the in-flight upload cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	id, err := suno.Upload(ctx, suno.UploadOptions{
		FilePath: *file,
		Token:    tok,
		DeviceID: dev,
	}, stdoutReporter{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nLỖI: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n✓ Upload thành công. clip id: %s\n", id)
	fmt.Println("Mở suno.com → Library/Workspace để xem clip vừa lên.")
}

// randomUUID returns a v4 UUID. Good enough for a header value; no external dep.
func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not something this harness needs to survive.
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
