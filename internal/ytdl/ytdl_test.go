package ytdl

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseProgress(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantPct    float64
		wantDetail string
	}{
		{
			name:       "full line",
			line:       " 42.3%|1.20MiB|2.80MiB|500.00KiB/s|00:03",
			wantPct:    0.423,
			wantDetail: "1.20MiB / 2.80MiB  ·  500.00KiB/s  ·  còn 00:03",
		},
		{
			// yt-dlp writes N/A for fields it cannot estimate yet; those must
			// be dropped rather than shown to the user.
			name:       "unknown total and eta",
			line:       "  5.0%|100.00KiB|N/A|1.00MiB/s|N/A",
			wantPct:    0.05,
			wantDetail: "100.00KiB  ·  1.00MiB/s",
		},
		{
			name:       "everything unknown",
			line:       "  0.0%|N/A|N/A|N/A|N/A",
			wantPct:    0,
			wantDetail: "",
		},
		{
			name:       "complete",
			line:       "100.0%|3.20MiB|3.20MiB|8.00MiB/s|00:00",
			wantPct:    1,
			wantDetail: "3.20MiB / 3.20MiB  ·  8.00MiB/s  ·  còn 00:00",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pct, detail := parseProgress(tc.line)
			if diff := pct - tc.wantPct; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("pct = %v, want %v", pct, tc.wantPct)
			}
			if detail != tc.wantDetail {
				t.Errorf("detail = %q, want %q", detail, tc.wantDetail)
			}
		})
	}
}

func TestParseProgressMalformed(t *testing.T) {
	// A garbled template must degrade to "unknown" rather than panic.
	if pct, _ := parseProgress("nonsense"); pct != -1 {
		t.Errorf("pct = %v, want -1 for unparseable input", pct)
	}
}

func TestReadPathFile(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "paths.txt")

	// yt-dlp appends a line per item; we want the last real one.
	if err := os.WriteFile(name, []byte("/tmp/a.wav\n/tmp/b.wav\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readPathFile(name)
	if err != nil {
		t.Fatalf("readPathFile: %v", err)
	}
	if got != "/tmp/b.wav" {
		t.Errorf("got %q, want /tmp/b.wav", got)
	}

	// CRLF, as written on Windows.
	if err := os.WriteFile(name, []byte("C:\\out\\song.wav\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = readPathFile(name)
	if err != nil {
		t.Fatalf("readPathFile: %v", err)
	}
	if got != "C:\\out\\song.wav" {
		t.Errorf("got %q, want the Windows path without the CR", got)
	}

	// An empty file means yt-dlp skipped post-processing; that must be an error
	// rather than an empty path handed back to the caller.
	if err := os.WriteFile(name, []byte("\n \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readPathFile(name); err == nil {
		t.Error("expected an error for a file with no path")
	}
}

func TestSharedArgs(t *testing.T) {
	base := strings.Join(sharedArgs(Options{}), " ")
	if strings.Contains(base, "--cookies-from-browser") || strings.Contains(base, "--js-runtimes") {
		t.Errorf("optional flags leaked in when unset: %q", base)
	}

	got := strings.Join(sharedArgs(Options{
		CookiesFromBrowser: "  firefox ",
		JSRuntime:          "node:/usr/bin/node",
	}), " ")
	if !strings.Contains(got, "--cookies-from-browser firefox") {
		t.Errorf("cookie flag missing or untrimmed: %q", got)
	}
	if !strings.Contains(got, "--js-runtimes node:/usr/bin/node") {
		t.Errorf("js runtime flag missing: %q", got)
	}
}

func TestValidateURL(t *testing.T) {
	ok := []string{
		"https://www.youtube.com/watch?v=abc123",
		"http://youtu.be/abc123",
		"  https://youtube.com/watch?v=x  ",
	}
	for _, in := range ok {
		got, err := validateURL(in)
		if err != nil {
			t.Errorf("validateURL(%q) errored: %v", in, err)
			continue
		}
		if strings.TrimSpace(in) != got {
			t.Errorf("validateURL(%q) = %q, want the trimmed input", in, got)
		}
	}

	// Anything that yt-dlp's parser would read as an option must be refused
	// here: yt-dlp accepts --exec, which runs a shell command, so a pasted
	// "--exec=..." in the URL box would otherwise be arbitrary code execution.
	bad := []string{
		"",
		"   ",
		"--version",
		"--exec=touch /tmp/pwned",
		"-o/tmp/x",
		"file:///etc/passwd",
		"ftp://example.com/a.mp3",
		"/home/user/local.mp3",
		"www.youtube.com/watch?v=x", // no scheme
		"https://",                  // no host
	}
	for _, in := range bad {
		if _, err := validateURL(in); err == nil {
			t.Errorf("validateURL(%q) should have been rejected", in)
		}
	}
}

func TestExplainFailure(t *testing.T) {
	base := errors.New("yt-dlp failed: exit status 1")

	// The 403 the user actually hit: yt-dlp too old for YouTube's defences.
	err := explainFailure(base, []string{
		"ERROR: unable to download video data: HTTP Error 403: Forbidden",
	}, true, SourceYouTube)
	if !errors.Is(err, base) {
		t.Error("explainFailure must wrap the original error")
	}
	if !strings.Contains(err.Error(), "Cập nhật yt-dlp") {
		t.Errorf("expected advice to update yt-dlp, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "JS runtime") {
		t.Error("must not mention a missing JS runtime when one is present")
	}

	// Same failure with no runtime installed should also say so.
	err = explainFailure(base, []string{"po token which was not provided"}, false, SourceYouTube)
	if !strings.Contains(err.Error(), "JS runtime") {
		t.Errorf("expected the missing-runtime hint, got %q", err.Error())
	}

	// An unrelated failure must pass through untouched, so we do not blame
	// yt-dlp's version for e.g. a full disk.
	err = explainFailure(base, []string{"ERROR: No space left on device"}, true, SourceYouTube)
	if err.Error() != base.Error() {
		t.Errorf("unrelated failure was rewritten: %q", err.Error())
	}
}

// The stale-yt-dlp advice is about YouTube's defences. A 403 from a CDN that a
// non-YouTube resolver pointed us at means something else entirely, and telling
// the user to update yt-dlp would send them down the wrong path.
func TestExplainFailureOnlyAdvisesForYouTube(t *testing.T) {
	base := errors.New("yt-dlp failed: exit status 1")
	err := explainFailure(base, []string{"ERROR: HTTP Error 403: Forbidden"}, true, "Suno")
	if err.Error() != base.Error() {
		t.Errorf("non-YouTube failure was rewritten: %q", err.Error())
	}
}

func TestOutputTemplate(t *testing.T) {
	// With no title supplied, yt-dlp names the file from its own metadata.
	got := outputTemplate(Options{OutDir: "/out"})
	if want := filepath.Join("/out", "%(title).120B [%(id)s].%(ext)s"); got != want {
		t.Errorf("default template = %q, want %q", got, want)
	}

	tests := []struct {
		name string
		base string
		want string
	}{
		{"plain", "E LÀ KHÔNG THỂ", "E LÀ KHÔNG THỂ.%(ext)s"},
		// Separators and the Windows-reserved set would either escape OutDir or
		// make the file unwritable on Windows.
		{"illegal characters", `a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j.%(ext)s"},
		// A bare % would be read by yt-dlp as the start of a template field.
		{"percent is escaped", "100% Love", "100%% Love.%(ext)s"},
		{"trailing dots and spaces", "  song ... ", "song.%(ext)s"},
		{"control characters", "a\nb", "a_b.%(ext)s"},
		// Blank after cleaning must fall back rather than yield ".%(ext)s".
		{"nothing left", " . ", "%(title).120B [%(id)s].%(ext)s"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := outputTemplate(Options{OutDir: "/out", FilenameBase: tc.base})
			if want := filepath.Join("/out", tc.want); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// A long multi-byte title must be cut on a rune boundary, or the filename ends
// in a broken UTF-8 sequence.
func TestOutputTemplateTruncatesOnRuneBoundary(t *testing.T) {
	long := strings.Repeat("ổ", 200) // 3 bytes each
	got := outputTemplate(Options{OutDir: "/out", FilenameBase: long})
	base := strings.TrimSuffix(filepath.Base(got), ".%(ext)s")
	if len(base) > 120 {
		t.Errorf("base is %d bytes, want <= 120", len(base))
	}
	if !utf8.ValidString(base) {
		t.Errorf("truncation split a rune: %q", base)
	}
}
