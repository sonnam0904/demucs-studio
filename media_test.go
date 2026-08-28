package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// newTestMediaServer returns a started server plus a request helper that
// carries the right token.
func newTestMediaServer(t *testing.T) (*mediaServer, func(path string) *httptest.ResponseRecorder) {
	t.Helper()
	m, err := newMediaServer()
	if err != nil {
		t.Fatalf("newMediaServer: %v", err)
	}
	get := func(path string) *httptest.ResponseRecorder {
		q := url.Values{}
		q.Set("p", path)
		q.Set("t", m.token)
		req := httptest.NewRequest(http.MethodGet, mediaPrefix+"?"+q.Encode(), nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)
		return rec
	}
	return m, get
}

// The loopback listener is the only delivery path WebKitGTK can actually play
// media from, so a failure to bind means silent no-audio.
func TestMediaServerStart(t *testing.T) {
	m, _ := newTestMediaServer(t)
	if m.BaseURL() != "" {
		t.Errorf("BaseURL should be empty before Start, got %q", m.BaseURL())
	}
	// URL must not hand out something unusable before the listener exists.
	if got := m.URL("/tmp/x.wav"); got != "" {
		t.Errorf("URL before Start = %q, want empty", got)
	}
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	base := m.BaseURL()
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Errorf("BaseURL = %q, want a loopback origin", base)
	}
}

// A loopback port is reachable by any local process, so the token is the first
// line of defence.
func TestMediaServerRequiresToken(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "vocals.wav")
	if err := os.WriteFile(file, []byte("RIFFfake"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, get := newTestMediaServer(t)
	m.Allow(file)

	if rec := get(file); rec.Code != http.StatusOK {
		t.Errorf("with token: status = %d, want 200", rec.Code)
	}

	for _, bad := range []string{"", "wrong", m.token + "x"} {
		q := url.Values{}
		q.Set("p", file)
		if bad != "" {
			q.Set("t", bad)
		}
		req := httptest.NewRequest(http.MethodGet, mediaPrefix+"?"+q.Encode(), nil)
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("token %q: status = %d, want 403", bad, rec.Code)
		}
	}
}

// Seeking in a long stem depends on Range support.
func TestMediaServerSupportsRange(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "vocals.wav")
	body := strings.Repeat("A", 5000)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestMediaServer(t)
	m.Allow(file)

	q := url.Values{}
	q.Set("p", file)
	q.Set("t", m.token)
	req := httptest.NewRequest(http.MethodGet, mediaPrefix+"?"+q.Encode(), nil)
	req.Header.Set("Range", "bytes=0-1445")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec.Code)
	}
	if got := rec.Body.Len(); got != 1446 {
		t.Errorf("body length = %d, want 1446", got)
	}
	if cr := rec.Header().Get("Content-Range"); cr != "bytes 0-1445/5000" {
		t.Errorf("Content-Range = %q", cr)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Errorf("Content-Type = %q, want audio/wav", ct)
	}
}

func TestMediaServerOnlyServesAllowedFiles(t *testing.T) {
	dir := t.TempDir()
	allowed := filepath.Join(dir, "vocals.wav")
	secret := filepath.Join(dir, "secret.wav")
	for _, p := range []string{allowed, secret} {
		if err := os.WriteFile(p, []byte("RIFFfake-wav-data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m, get := newTestMediaServer(t)
	m.Allow(allowed)

	if rec := get(allowed); rec.Code != http.StatusOK {
		t.Errorf("allowed file: status = %d, want 200", rec.Code)
	} else if rec.Body.String() != "RIFFfake-wav-data" {
		t.Errorf("allowed file: body = %q", rec.Body.String())
	}

	// The allow-list is the only thing standing between the webview and the
	// whole filesystem, so a sibling file must be refused.
	if rec := get(secret); rec.Code != http.StatusForbidden {
		t.Errorf("unlisted file: status = %d, want 403", rec.Code)
	}
	if rec := get("/etc/passwd"); rec.Code != http.StatusForbidden {
		t.Errorf("/etc/passwd: status = %d, want 403", rec.Code)
	}

	// Traversal must not launder a path onto the allow-list.
	if rec := get(filepath.Join(dir, "sub", "..", "secret.wav")); rec.Code != http.StatusForbidden {
		t.Errorf("traversal: status = %d, want 403", rec.Code)
	}
	// ...but the equivalent form of an allowed path is fine, since canonical()
	// normalises before comparing.
	if rec := get(filepath.Join(dir, ".", "vocals.wav")); rec.Code != http.StatusOK {
		t.Errorf("equivalent allowed path: status = %d, want 200", rec.Code)
	}
}

func TestMediaServerMissingParam(t *testing.T) {
	m, _ := newTestMediaServer(t)
	q := url.Values{}
	q.Set("t", m.token)
	req := httptest.NewRequest(http.MethodGet, mediaPrefix+"?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestMediaServerAllowedButDeleted(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone.wav")
	m, get := newTestMediaServer(t)
	m.Allow(gone)

	rec := get(gone)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestMediaServerServesDirectoryAsNotFound(t *testing.T) {
	dir := t.TempDir()
	m, get := newTestMediaServer(t)
	m.Allow(dir)

	rec := get(dir)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a directory", rec.Code)
	}
}

func TestMediaURLIsParseable(t *testing.T) {
	m, _ := newTestMediaServer(t)
	if err := m.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Titles routinely contain spaces, brackets and non-ASCII.
	path := "/home/x/Nhạc/Bài hát [abc123].wav"
	raw := m.URL(path)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("URL(%q) produced %q: %v", path, raw, err)
	}
	if got := u.Query().Get("p"); got != path {
		t.Errorf("round-trip: got %q, want %q", got, path)
	}
	if u.Query().Get("t") != m.token {
		t.Error("URL must carry the token")
	}
	if u.Scheme != "http" || u.Hostname() != "127.0.0.1" {
		t.Errorf("URL must be an absolute loopback URL, got %q", raw)
	}
}

func TestContentType(t *testing.T) {
	tests := map[string]string{
		"a.wav":  "audio/wav",
		"a.WAV":  "audio/wav",
		"a.mp3":  "audio/mpeg",
		"a.flac": "audio/flac",
		"a.txt":  "",
	}
	for in, want := range tests {
		if got := contentType(in); got != want {
			t.Errorf("contentType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeName(t *testing.T) {
	tests := map[string]string{
		// Characters Windows forbids in filenames.
		`Song: "Title" <live> | 2024/07`: "Song_ _Title_ _live_ _ 2024_07",
		"  padded  ":                     "padded",
		"trailing dots...":               "trailing dots",
		"":                               "track",
		"...":                            "track",
		"Bài hát tiếng Việt":             "Bài hát tiếng Việt",
	}
	for in, want := range tests {
		if got := safeName(in); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}

	long := safeName(strings.Repeat("a", 400))
	if len(long) > maxNameBytes {
		t.Errorf("safeName should cap length, got %d", len(long))
	}
}

// Truncation must land on a rune boundary. A byte-slice cut through a multi-byte
// rune yields a directory name that is not valid UTF-8, which survives on Linux
// but comes back through encoding/json as U+FFFD — so every path the frontend
// then holds refers to a file that does not exist.
func TestSafeNameTruncatesOnRuneBoundary(t *testing.T) {
	// Each of these is 3 bytes, so the cap lands mid-rune for some lengths.
	for _, unit := range []string{"á", "ệ", "이", "音", "🎵"} {
		for n := 30; n <= 80; n++ {
			in := strings.Repeat(unit, n)
			got := safeName(in)
			if !utf8.ValidString(got) {
				t.Fatalf("safeName(%d×%q) produced invalid UTF-8: %q", n, unit, got)
			}
			if len(got) > maxNameBytes {
				t.Fatalf("safeName(%d×%q) is %d bytes, over the cap", n, unit, len(got))
			}
		}
	}
}

// The trailing-punctuation trim has to run after the cut, because a cut can
// expose a new trailing '.' or ' ' — which Windows rejects outright.
func TestSafeNameTrimsAfterTruncating(t *testing.T) {
	// Land a '.' exactly on the boundary.
	in := strings.Repeat("a", maxNameBytes-1) + "." + strings.Repeat("b", 20)
	got := safeName(in)
	if strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ") {
		t.Errorf("safeName left trailing punctuation: %q", got)
	}

	in = strings.Repeat("a", maxNameBytes-1) + "  tail"
	if got := safeName(in); strings.HasSuffix(got, " ") {
		t.Errorf("safeName left a trailing space: %q", got)
	}
}

// The real case that motivated this: a long CJK YouTube title.
func TestSafeNameRealWorldTitle(t *testing.T) {
	in := "ECLIPSE (이클립스) - Sudden Shower (소나기) ｜ Lovely Runner (선재업고 튀어) OST Part. 1 (ENG) MV [DpKI0zDPqRQ]"
	got := safeName(in)
	if !utf8.ValidString(got) {
		t.Errorf("invalid UTF-8: %q", got)
	}
	if got == "" || got == "track" {
		t.Errorf("title collapsed to %q", got)
	}
	if len(got) > maxNameBytes {
		t.Errorf("%d bytes, over the cap", len(got))
	}
}

func TestBaseNameNoExt(t *testing.T) {
	tests := map[string]string{
		"/tmp/a/song.wav":    "song",
		"/tmp/a/song.tar.gz": "song.tar",
		"/tmp/a/noext":       "noext",
		"song [id].wav":      "song [id]",
	}
	for in, want := range tests {
		if got := baseNameNoExt(in); got != want {
			t.Errorf("baseNameNoExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	tests := map[float64]string{
		0:    "?",
		-1:   "?",
		19:   "0:19",
		95:   "1:35",
		3600: "1:00:00",
		3725: "1:02:05",
	}
	for in, want := range tests {
		if got := formatDuration(in); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1536:       "1.5 KB",
		84141271:   "80.2 MB",
		3221225472: "3.0 GB",
	}
	for in, want := range tests {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}
