// Package netfetch downloads files over HTTP with progress reporting and
// checksum verification. Used for model weights and helper binaries.
package netfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProgressFunc is called periodically with bytes transferred so far. total is 0
// when the server does not advertise a Content-Length.
type ProgressFunc func(done, total int64)

var client = &http.Client{Timeout: 0} // large files; rely on ctx for deadlines

// Get fetches url into dest atomically: it writes to dest+".part" and renames
// only after the body is fully read and any checksum matches.
//
// sha256Prefix, when non-empty, is compared case-insensitively against the
// leading hex digits of the file's SHA-256. Demucs names its weights
// "<signature>-<sha256 prefix>.th" and verifies exactly this, so passing the
// prefix parsed out of the filename gives us the same guarantee without having
// to hardcode a hash table.
func Get(ctx context.Context, url, dest, sha256Prefix string, onProgress ProgressFunc) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".part"
	// A leftover .part from an interrupted run must not be appended to.
	_ = os.Remove(tmp)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "DemucsStudio")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	hash := sha256.New()
	var writer io.Writer = f
	if sha256Prefix != "" {
		writer = io.MultiWriter(f, hash)
	}

	counter := &progressWriter{
		target:  writer,
		total:   resp.ContentLength,
		report:  onProgress,
		nextAt:  time.Now(),
		minGap:  150 * time.Millisecond,
		context: ctx,
	}
	_, copyErr := io.Copy(counter, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}

	// A body cut short by a proxy or a dropped connection can surface as a clean
	// EOF. Without this check the truncated file gets renamed onto the
	// destination and — for the helper binaries — made executable.
	if resp.ContentLength > 0 && counter.done != resp.ContentLength {
		_ = os.Remove(tmp)
		return fmt.Errorf("tải %s bị thiếu: nhận %d byte, server báo %d",
			filepath.Base(dest), counter.done, resp.ContentLength)
	}

	if sha256Prefix != "" {
		got := hex.EncodeToString(hash.Sum(nil))
		if len(sha256Prefix) > len(got) {
			_ = os.Remove(tmp)
			return errors.New("checksum prefix longer than a sha256 digest")
		}
		if !strings.EqualFold(got[:len(sha256Prefix)], sha256Prefix) {
			_ = os.Remove(tmp)
			return fmt.Errorf("checksum mismatch for %s: expected prefix %s, got %s",
				filepath.Base(dest), sha256Prefix, got[:len(sha256Prefix)])
		}
	}

	if onProgress != nil {
		onProgress(counter.done, counter.total)
	}
	// Windows refuses to rename onto an existing file.
	_ = os.Remove(dest)
	return os.Rename(tmp, dest)
}

// VerifyFile checks an already-present file against a SHA-256 hex prefix.
func VerifyFile(path, sha256Prefix string) error {
	if sha256Prefix == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if len(sha256Prefix) > len(got) {
		return errors.New("checksum prefix longer than a sha256 digest")
	}
	if !strings.EqualFold(got[:len(sha256Prefix)], sha256Prefix) {
		return fmt.Errorf("checksum mismatch for %s: expected prefix %s, got %s",
			filepath.Base(path), sha256Prefix, got[:len(sha256Prefix)])
	}
	return nil
}

// ChecksumList fetches a `sha256  filename` manifest (the format `sha256sum`
// writes, which both the yt-dlp and FFmpeg-Builds releases publish) and returns
// the digest recorded for want.
//
// The filename must match exactly: yt-dlp's SHA2-256SUMS contains five entries
// whose names merely *contain* "yt-dlp_linux" (…_aarch64, …_armv7l, .zip, …), so
// a substring match would happily verify the wrong asset.
//
// This does not defend against a compromised release host — the manifest lives
// beside the asset — but it does catch truncation, proxy corruption, and an
// asset/manifest mismatch, which are the realistic failures.
func ChecksumList(ctx context.Context, manifestURL, want string) (string, error) {
	raw, err := Fetch(ctx, manifestURL)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// sha256sum marks binary mode with a leading '*' on the name.
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name == want {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("không thấy %s trong %s", want, manifestURL)
}

// Fetch returns a small response body, with a short timeout.
func Fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "DemucsStudio")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

type progressWriter struct {
	target  io.Writer
	total   int64
	done    int64
	report  ProgressFunc
	nextAt  time.Time
	minGap  time.Duration
	context context.Context
}

func (w *progressWriter) Write(p []byte) (int, error) {
	if err := w.context.Err(); err != nil {
		return 0, err
	}
	n, err := w.target.Write(p)
	w.done += int64(n)
	if w.report != nil && time.Now().After(w.nextAt) {
		w.nextAt = time.Now().Add(w.minGap)
		w.report(w.done, w.total)
	}
	return n, err
}
