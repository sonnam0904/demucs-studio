package suno

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIsURL(t *testing.T) {
	yes := []string{
		"https://suno.com/song/080b4f6b-0064-4d2e-936a-5f1dec5caba8?sh=Aw7BG9Uyb2b1F554",
		"https://www.suno.com/song/abc",
		"http://suno.com/s/short",
		"https://app.suno.ai/song/abc",
		"  https://suno.com/song/abc  ",
	}
	for _, u := range yes {
		if !IsURL(u) {
			t.Errorf("IsURL(%q) = false, want true", u)
		}
	}
	no := []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"",
		"not a url",
		// The host has to end at the domain: a look-alike must not match.
		"https://suno.com.evil.example/song/abc",
		"https://notsuno.com/song/abc",
		// Non-http schemes are rejected before anything else looks at them.
		"file:///etc/passwd",
	}
	for _, u := range no {
		if IsURL(u) {
			t.Errorf("IsURL(%q) = true, want false", u)
		}
	}
}

// jsEscape applies the one level of escaping Next.js adds when it embeds its
// JSON payload inside a JavaScript string literal.
var jsEscape = strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace

// page wraps a clip object the way a real song page carries it.
func page(clipJSON string) string {
	return `<!DOCTYPE html><html><head><meta property="og:title" content="x"/></head><body>` +
		`<script>self.__next_f.push([1,"` +
		jsEscape(`41:["$","$L51",null,{"clip":`+clipJSON+`}]`) +
		`\n"])</script></body></html>`
}

// The real payload, reduced to the fields we read. The title carries a quote
// and a brace so the brace matcher has to honour string boundaries, and the
// metadata carries a JSON \n escape, which survives only if unescapeJS leaves
// escapes other than \\ and \" alone.
const realClip = `{"status":"complete","title":"Bài \"hát\" {lofi}",` +
	`"id":"080b4f6b-0064-4d2e-936a-5f1dec5caba8",` +
	`"video_url":"https://cdn1.suno.ai/080b4f6b.mp4",` +
	`"audio_url":"https://studio-api.prod.suno.com/api/forbidden",` +
	`"media_urls":[{"url":"https://cdn.example/clip.m4a","content_type":"m4a-opus"}],` +
	`"image_url":"https://cdn2.suno.ai/image_080b4f6b.jpeg",` +
	`"image_large_url":"https://cdn2.suno.ai/image_large_080b4f6b.jpeg",` +
	`"metadata":{"tags":"lo-fi\nballad","duration":329.6},` +
	`"display_name":"Lan","handle":"hoanglan"}`

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExtractClip(t *testing.T) {
	c, err := extractClip(page(realClip))
	if err != nil {
		t.Fatalf("extractClip: %v", err)
	}
	if want := `Bài "hát" {lofi}`; c.Title != want {
		t.Errorf("Title = %q, want %q", c.Title, want)
	}
	if c.ID != "080b4f6b-0064-4d2e-936a-5f1dec5caba8" {
		t.Errorf("ID = %q", c.ID)
	}
	if c.Metadata.Duration != 329.6 {
		t.Errorf("Duration = %v, want 329.6", c.Metadata.Duration)
	}
	if c.uploader() != "Lan" {
		t.Errorf("uploader = %q, want Lan", c.uploader())
	}
}

func TestExtractClipMissing(t *testing.T) {
	if _, err := extractClip("<html><body>nothing here</body></html>"); err == nil {
		t.Error("expected an error when the page carries no clip payload")
	}
	// A payload cut off mid-object must be reported, not parsed into a
	// half-filled clip that then yields a bogus media URL.
	truncated := page(realClip)
	if _, err := extractClip(truncated[:len(truncated)/2]); err == nil {
		t.Error("expected an error for a truncated payload")
	}
}

// audio_url is preferred, but a signed-out visitor gets Suno's /api/forbidden
// placeholder there — taking it at face value would hand yt-dlp a URL that
// serves an error page.
func TestMediaURLSkipsForbiddenPlaceholder(t *testing.T) {
	c, err := extractClip(page(realClip))
	if err != nil {
		t.Fatalf("extractClip: %v", err)
	}
	if got := c.mediaURL(context.Background()); got != "https://cdn1.suno.ai/080b4f6b.mp4" {
		t.Errorf("mediaURL = %q, want the video_url", got)
	}
}

// media_urls is last on purpose, and is only reached when the plain URLs are
// gone — it is Suno's encrypted player stream far more often than a file.
func TestMediaURLPrefersPlainURLs(t *testing.T) {
	var c clip
	c.VideoURL = "https://cdn1.suno.ai/x.mp4"
	c.MediaURLs = append(c.MediaURLs, struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	}{URL: "https://cdn.example/x.m4a", ContentType: "m4a-opus"})
	// No probe should happen here: video_url wins outright, and reaching the
	// network for an unreachable host would hang or fail the test.
	if got := c.mediaURL(context.Background()); got != c.VideoURL {
		t.Errorf("mediaURL = %q, want %q", got, c.VideoURL)
	}

	c.AudioURL = "https://cdn1.suno.ai/x.mp3"
	if got := c.mediaURL(context.Background()); got != c.AudioURL {
		t.Errorf("a real audio_url must win, got %q", got)
	}
}

// The failing case that prompted this: a private song has no video_url, and its
// one media_urls entry serves encrypted bytes. Trusting it cost a multi-megabyte
// download that died in yt-dlp post-processing with "unable to obtain file audio
// codec" — no hint that the song's privacy was the problem.
func TestMediaURLRejectsEncryptedStream(t *testing.T) {
	encrypted := serveBytes(t, []byte{0xa4, 0x8e, 0xee, 0xbd, 0x7b, 0x56, 0xb3, 0x53,
		0xf6, 0xec, 0xa8, 0x79, 0x6d, 0x47, 0x4b, 0xd1})
	var c clip
	c.MediaURLs = append(c.MediaURLs, struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	}{URL: encrypted, ContentType: "m4a-opus"})
	if got := c.mediaURL(context.Background()); got != "" {
		t.Errorf("mediaURL = %q, want empty for an encrypted stream", got)
	}

	// A real container at the same spot must still be used.
	real := serveBytes(t, append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A ....")...))
	c.MediaURLs[0].URL = real
	if got := c.mediaURL(context.Background()); got != real {
		t.Errorf("mediaURL = %q, want %q", got, real)
	}
}

func TestIsContainer(t *testing.T) {
	yes := map[string][]byte{
		"mp4/m4a": append([]byte{0, 0, 0, 0x20}, []byte("ftypM4A ")...),
		"mp3 id3": []byte("ID3\x04\x00\x00\x00\x00\x00"),
		"ogg":     []byte("OggS\x00\x02\x00\x00"),
		"wav":     []byte("RIFF\x24\x08\x00\x00WAVE"),
		"flac":    []byte("fLaC\x00\x00\x00\x22"),
		"mp3 raw": {0xFF, 0xFB, 0x90, 0x64},
	}
	for name, b := range yes {
		if !isContainer(b) {
			t.Errorf("%s: isContainer = false, want true", name)
		}
	}
	no := map[string][]byte{
		"suno encrypted": {0xa4, 0x8e, 0xee, 0xbd, 0x7b, 0x56, 0xb3, 0x53},
		"html error":     []byte("<!DOCTYPE html>"),
		"empty":          {},
		"one byte":       {0xFF},
	}
	for name, b := range no {
		if isContainer(b) {
			t.Errorf("%s: isContainer = true, want false", name)
		}
	}
}

// A non-public song gets an extra instruction, but the message must never claim
// the song is inaccessible: is_public=false still opens and still plays for
// anyone with the link, so "riêng tư" would contradict what the user sees.
func TestUnavailableErr(t *testing.T) {
	no, yes := false, true

	notPublic := (clip{IsPublic: &no}).unavailableErr().Error()
	if !strings.Contains(notPublic, "Public") {
		t.Errorf("a non-public song should say how to fix it: %v", notPublic)
	}
	for _, err := range []string{
		notPublic,
		(clip{IsPublic: &yes}).unavailableErr().Error(),
		(clip{}).unavailableErr().Error(),
	} {
		if strings.Contains(err, "riêng tư") {
			t.Errorf("must not claim the song is inaccessible: %v", err)
		}
		// Every variant has to name a way out, or it is just a dead end.
		if !strings.Contains(err, "chọn file audio có sẵn") {
			t.Errorf("no actionable fallback offered: %v", err)
		}
	}
}

// serveBytes returns a URL serving exactly these bytes, honouring Range so the
// probe reads only the head.
func serveBytes(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "clip.m4a", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/clip.m4a"
}

func TestResolve(t *testing.T) {
	srv := serve(t, page(realClip))
	// The host check runs on the URL the caller passed, so point it at the test
	// server through the same code path a real link takes.
	oldClient := client
	client = srv.Client()
	t.Cleanup(func() { client = oldClient })
	client.Transport = rewriteHost{srv.Listener.Addr().String()}

	song, err := Resolve(context.Background(), "https://suno.com/song/080b4f6b")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if song.Title != `Bài "hát" {lofi}` {
		t.Errorf("Title = %q", song.Title)
	}
	if song.Uploader != "Lan" {
		t.Errorf("Uploader = %q", song.Uploader)
	}
	if song.Duration != 329.6 {
		t.Errorf("Duration = %v", song.Duration)
	}
	if song.MediaURL != "https://cdn1.suno.ai/080b4f6b.mp4" {
		t.Errorf("MediaURL = %q", song.MediaURL)
	}
	if song.Thumbnail != "https://cdn2.suno.ai/image_large_080b4f6b.jpeg" {
		t.Errorf("Thumbnail = %q", song.Thumbnail)
	}
}

func TestResolveRejectsNonSuno(t *testing.T) {
	if _, err := Resolve(context.Background(), "https://youtube.com/watch?v=x"); err == nil {
		t.Error("Resolve must refuse a non-Suno URL rather than fetch it")
	}
}

// rewriteHost sends every request to the test server while leaving the request
// URL — and therefore the host check — as the caller wrote it.
type rewriteHost struct{ addr string }

func (h rewriteHost) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = h.addr
	return http.DefaultTransport.RoundTrip(clone)
}

func TestUnescapeJS(t *testing.T) {
	tests := []struct{ in, want string }{
		{`\"a\"`, `"a"`},
		// A JSON escape arrives doubled and must come out as a single JSON
		// escape, not as the character it denotes.
		{`\\n`, `\n`},
		{`\\\"`, `\"`},
		{`\\\\`, `\\`},
		// Escapes we do not handle pass through untouched; JSON decodes them.
		{`<`, `<`},
		{`plain`, `plain`},
	}
	for _, tc := range tests {
		if got := unescapeJS(tc.in); got != tc.want {
			t.Errorf("unescapeJS(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestObjectAt(t *testing.T) {
	s := `x = {"a":{"b":"}"},"c":1} tail`
	got, ok := objectAt(s, 4)
	if !ok || got != `{"a":{"b":"}"},"c":1}` {
		t.Errorf("objectAt = %q, %v", got, ok)
	}
	if _, ok := objectAt(s, 0); ok {
		t.Error("objectAt must refuse a start that is not a brace")
	}
	if _, ok := objectAt(`{"a":1`, 0); ok {
		t.Error("objectAt must refuse an unclosed object")
	}
}

// Opt-in smoke test against the live site, since the page layout is Suno's to
// change and nothing else here would notice:
//
//	SUNO_LIVE_URL="https://suno.com/song/…" go test ./internal/suno -run Live -v
func TestLiveResolve(t *testing.T) {
	raw := os.Getenv("SUNO_LIVE_URL")
	if raw == "" {
		t.Skip("set SUNO_LIVE_URL to run")
	}
	song, err := Resolve(context.Background(), raw)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if song.Title == "" || song.MediaURL == "" {
		t.Errorf("incomplete song: %+v", song)
	}
	t.Logf("%+v", song)
}
