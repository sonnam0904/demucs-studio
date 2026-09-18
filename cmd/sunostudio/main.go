// Command sunostudio is a throwaway harness for the Suno Studio "reverse" flow.
//
// Its first job is to confirm the render-state plumbing works against the live
// API by replaying a captured request body (internal/suno/testdata) with a fresh
// token. Success = the response's title contains "reversed".
//
// Usage:
//
//	go run ./cmd/sunostudio -token-file /tmp/suno-token.txt
//	go run ./cmd/sunostudio -token-file /tmp/suno-token.txt -request-file some.json
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

func main() {
	token := flag.String("token", "", "Bearer JWT from a logged-in suno.com session")
	tokenFile := flag.String("token-file", "", "path to a file holding the Bearer JWT")
	device := flag.String("device", "", "device-id header (a random UUID is used if empty)")
	requestFile := flag.String("request-file", "internal/suno/testdata/render_state_request.json",
		"render-state request body to replay (when -clip is not set)")
	clipID := flag.String("clip", "", "uploaded clip id: run the full create-project → reverse → render flow")
	title := flag.String("title", "", "clip title (defaults to the clip's own title)")
	keepWarp := flag.Bool("keep-warp", false, "reuse the template's warp markers verbatim (only valid when -clip is the SAME audio as the template)")
	flag.Parse()

	raw := *token
	if strings.TrimSpace(*tokenFile) != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "không đọc được token-file: %v\n", err)
			os.Exit(2)
		}
		raw = string(b)
	} else if strings.TrimSpace(raw) == "" {
		raw = os.Getenv("SUNO_TOKEN")
	}
	tok := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "Bearer "))
	if tok == "" {
		fmt.Fprintln(os.Stderr, "cần token (-token / -token-file / $SUNO_TOKEN)")
		os.Exit(2)
	}
	if strings.ContainsAny(tok, " \n\t…") {
		fmt.Fprintln(os.Stderr, "⚠ token có khoảng trắng/xuống dòng/dấu … — có thể đã bị cắt.")
	}

	dev := strings.TrimSpace(*device)
	if dev == "" {
		dev = randomUUID()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	auth := suno.StudioAuth{Token: tok, DeviceID: dev}

	var clip suno.RenderedClip
	var err error
	if strings.TrimSpace(*clipID) != "" {
		// Full generalized flow: create project → reverse → render.
		fmt.Printf("→ create-project + reverse clip %s\n", *clipID)
		clip, err = suno.ReverseClip(ctx, nil, *clipID, strings.TrimSpace(*title), 0, *keepWarp, auth)
	} else {
		// Replay a captured render-state body verbatim.
		var body []byte
		body, err = os.ReadFile(*requestFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "không đọc được request-file: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("→ POST render-state (replay %s, %d bytes)\n", *requestFile, len(body))
		clip, err = suno.RenderStateRaw(ctx, nil, body, auth)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nLỖI: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n✓ render-state OK\n  id:     %s\n  title:  %s\n  status: %s\n", clip.ID, clip.Title, clip.Status)

	// "queued" is async — poll until it actually finishes (or fails) so we know
	// whether a clip really lands in the library.
	fmt.Println("\n→ chờ render hoàn tất…")
	final, perr := suno.PollRender(ctx, nil, clip.ID, auth, stepPrinter{})
	if perr != nil {
		fmt.Fprintf(os.Stderr, "\n❌ render KHÔNG hoàn tất: %v (status cuối: %s)\n", perr, final.Status)
		os.Exit(1)
	}
	fmt.Printf("\n✅ render xong: status=%s\n", final.Status)
	fmt.Printf("  clip:     %s\n", final.PageURL())
	fmt.Printf("  duration: %.1fs\n", final.Metadata.Duration)
	fmt.Printf("  media:    %s\n", final.PlayableURL())
	fmt.Println("\n→ Mở link 'clip' ở trên để nghe thử (audio thật nằm ở media, không phải audio_url).")
}

// stepPrinter forwards PollRender progress to stdout.
type stepPrinter struct{}

func (stepPrinter) Log(level, text string)                 {}
func (stepPrinter) Logf(level, format string, a ...any)    {}
func (stepPrinter) Step(phase string, f float64, label, detail string) {
	fmt.Printf("  … %s %s\n", label, detail)
}

func randomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
