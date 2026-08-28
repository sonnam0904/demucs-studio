package netfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A body cut short can surface as a clean EOF. Before the length check, the
// truncated file was renamed onto the destination and — for the helper binaries
// — made executable, leaving yt-dlp permanently broken with an opaque error.
func TestGetRejectsTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("only a few bytes"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "yt-dlp")
	err := Get(context.Background(), srv.URL, dest, "", nil)
	if err == nil {
		t.Fatal("expected an error for a truncated body")
	}
	if _, statErr := os.Stat(dest); statErr == nil {
		t.Error("a truncated download must not be left at the destination")
	}
	if _, statErr := os.Stat(dest + ".part"); statErr == nil {
		t.Error("the .part file must be cleaned up")
	}
}

func TestGetAcceptsCompleteBody(t *testing.T) {
	body := "complete payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "ok.bin")
	if err := Get(context.Background(), srv.URL, dest, "", nil); err != nil {
		t.Fatalf("Get: %v", err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil || string(raw) != body {
		t.Errorf("got %q, %v", raw, err)
	}
}

func TestGetVerifiesChecksum(t *testing.T) {
	body := "hello"
	// sha256("hello")
	const full = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := Get(context.Background(), srv.URL, filepath.Join(dir, "a"), full, nil); err != nil {
		t.Errorf("correct digest rejected: %v", err)
	}
	bad := filepath.Join(dir, "b")
	if err := Get(context.Background(), srv.URL, bad, "0000000000000000", nil); err == nil {
		t.Error("wrong digest accepted")
	}
	if _, err := os.Stat(bad); err == nil {
		t.Error("a checksum failure must not leave the file behind")
	}
}

// yt-dlp's SHA2-256SUMS has five entries whose names merely *contain*
// "yt-dlp_linux", so a substring match would verify the wrong asset.
func TestChecksumListMatchesExactFilename(t *testing.T) {
	manifest := `58162f9bfdc27458ea47bfcb311cf47028f17d8154a8bf7d689861d46399230a  yt-dlp_linux
32e72032766bef9199d99d15beb69fd52e46df8f8b06f0d8745db59e04d339e9  yt-dlp_linux.zip
b16e4dab368a816cd05d477d698a605a6ae87ccee1c8ffd38fa21d7254141fcc  yt-dlp_linux_aarch64
66674953fe251b89f4d08c5f0e35e0728679bd67ab3d7d05c0562af101dd3e7a *yt-dlp.exe
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(manifest))
	}))
	defer srv.Close()

	ctx := context.Background()
	got, err := ChecksumList(ctx, srv.URL, "yt-dlp_linux")
	if err != nil {
		t.Fatalf("ChecksumList: %v", err)
	}
	if got != "58162f9bfdc27458ea47bfcb311cf47028f17d8154a8bf7d689861d46399230a" {
		t.Errorf("matched the wrong line: %s", got)
	}

	// sha256sum's binary-mode '*' prefix must be stripped.
	if got, err = ChecksumList(ctx, srv.URL, "yt-dlp.exe"); err != nil ||
		got != "66674953fe251b89f4d08c5f0e35e0728679bd67ab3d7d05c0562af101dd3e7a" {
		t.Errorf("binary-mode entry: got %q, %v", got, err)
	}

	if _, err := ChecksumList(ctx, srv.URL, "yt-dlp_macos"); err == nil {
		t.Error("expected an error for an absent asset")
	}
}

func TestVerifyFileRejectsOverlongPrefix(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 65 hex chars cannot be a sha256 prefix.
	long := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"
	if err := VerifyFile(p, long); err == nil {
		t.Error("expected an error for a prefix longer than a digest")
	}
	if err := VerifyFile(p, ""); err != nil {
		t.Errorf("empty prefix should skip verification, got %v", err)
	}
}
