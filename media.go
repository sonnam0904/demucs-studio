package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// mediaServer lets the webview play files that live on disk.
//
// It serves them over a loopback HTTP listener rather than through the Wails
// asset server, because WebKitGTK's media pipeline cannot play a resource
// delivered by a custom URI scheme. Measured on this app: fetch() of a
// wails://-served WAV returns 200 with the full 41 MB body, but assigning the
// same URL to an <audio> element fails with MEDIA_ERR_SRC_NOT_SUPPORTED
// (code 4) at readyState 0. The identical file over http://127.0.0.1 reaches
// readyState 4 with the correct duration. A blob: URL built from fetch() also
// works, but it has to buffer the whole file in memory — untenable here, where
// one job can produce six multi-hundred-megabyte WAV stems.
//
// Two things guard the listener, since any local process can connect to a
// loopback port:
//
//   - a per-run random token that every request must carry, and
//   - the allow-list: only files the backend produced or the user explicitly
//     picked are served, so knowing the port is not enough.
type mediaServer struct {
	token string

	mu      sync.RWMutex
	base    string // "http://127.0.0.1:39873", empty until Start succeeds
	allowed map[string]bool
}

const mediaPrefix = "/media"

func newMediaServer() (*mediaServer, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("không tạo được token cho media server: %w", err)
	}
	return &mediaServer{
		token:   hex.EncodeToString(raw),
		allowed: map[string]bool{},
	}, nil
}

// Start binds a loopback listener on an ephemeral port and serves media from it.
func (m *mediaServer) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("không mở được cổng loopback cho media: %w", err)
	}
	m.mu.Lock()
	m.base = "http://" + ln.Addr().String()
	m.mu.Unlock()

	server := &http.Server{Handler: m}
	go func() { _ = server.Serve(ln) }()
	return nil
}

// BaseURL is the listener's origin, or "" when Start has not succeeded.
func (m *mediaServer) BaseURL() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.base
}

// Allow registers a file as playable.
func (m *mediaServer) Allow(path string) {
	if path == "" {
		return
	}
	key := canonical(path)
	m.mu.Lock()
	m.allowed[key] = true
	m.mu.Unlock()
}

func (m *mediaServer) isAllowed(path string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.allowed[canonical(path)]
}

// URL builds an absolute URL for an allow-listed file, or "" if the server is
// not running. A modification-time parameter busts the webview cache when a
// stem is regenerated.
func (m *mediaServer) URL(path string) string {
	base := m.BaseURL()
	if base == "" || path == "" {
		return ""
	}
	q := url.Values{}
	q.Set("p", path)
	q.Set("t", m.token)
	if st, err := os.Stat(path); err == nil {
		q.Set("v", st.ModTime().UTC().Format("20060102150405"))
	}
	return base + mediaPrefix + "?" + q.Encode()
}

func (m *mediaServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, mediaPrefix) {
		http.NotFound(w, r)
		return
	}
	// Constant-time compare: the token is a secret, and a loopback port is
	// reachable by any local process.
	got := r.URL.Query().Get("t")
	if subtle.ConstantTimeCompare([]byte(got), []byte(m.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	path := r.URL.Query().Get("p")
	if path == "" {
		http.Error(w, "missing p", http.StatusBadRequest)
		return
	}
	if !m.isAllowed(path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if ct := contentType(path); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	// ServeContent implements Range requests, which the <audio> element needs
	// in order to seek.
	http.ServeContent(w, r, filepath.Base(path), st.ModTime(), f)
}

func contentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".ogg", ".opus":
		return "audio/ogg"
	}
	return ""
}

// canonical normalises a path for allow-list comparison. Windows paths are
// case-insensitive; POSIX ones are not.
func canonical(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if isWindows {
		return strings.ToLower(path)
	}
	return path
}
