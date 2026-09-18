// Upload drives Suno's private "upload audio" API — the same three calls the web
// client makes when you drag a file into Create → Upload Audio:
//
//  1. POST /api/uploads/audio/          → reserves an id and hands back a
//     presigned S3 POST (url + form fields).
//  2. POST <presigned S3 url>           → the raw file, multipart, `file` last.
//  3. POST /api/uploads/audio/<id>/upload-finish/  → tells Suno the bytes landed
//     and names the clip.
//
// Everything here is reverse-engineered from a real session's traffic, so it is
// inherently more fragile than the download path: the endpoints are private and
// can change without notice. Auth is a short-lived Bearer JWT the caller must
// supply — see the token-acquisition layer for where that comes from.
package suno

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// studioAPIBase is Suno's API host. Named separately so a future host change is
// one edit, and so tests can point it at a stub.
const studioAPIBase = "https://studio-api-prod.suno.com"

// Reporter is the subset of the event bus this package needs. It mirrors
// ytdl.Reporter so app.go's single reporter satisfies both.
type Reporter interface {
	Log(level, text string)
	Logf(level, format string, args ...any)
	Step(phase string, fraction float64, label, detail string)
}

// UploadOptions configures one upload.
type UploadOptions struct {
	// FilePath is the local audio file to send.
	FilePath string
	// Token is the Bearer JWT for studio-api — short-lived, supplied per call.
	Token string
	// DeviceID is the persistent device UUID the web client sends. Any stable
	// UUID works; it only needs to be constant for a given install.
	DeviceID string
	// Client lets a caller inject a test double; nil uses the package default,
	// which has no timeout so a large file is bounded by ctx, not a clock.
	Client *http.Client
}

// uploadClient has no timeout on purpose: a 500 MB file over a slow link would
// trip any fixed deadline, and cancellation already flows through ctx.
var uploadClient = &http.Client{}

// uploadInit is the reservation the first call returns. Fields is the presigned
// POST form — its keys (Content-Type, key, AWSAccessKeyId, policy, signature)
// become form fields verbatim, so it is kept as a map to survive Suno adding or
// renaming one.
type uploadInit struct {
	ID     string            `json:"id"`
	URL    string            `json:"url"`
	Fields map[string]string `json:"fields"`
}

// Upload runs the full three-step flow and returns the Suno upload id, which is
// the handle the clip is later known by.
func Upload(ctx context.Context, o UploadOptions, r Reporter) (string, error) {
	if strings.TrimSpace(o.Token) == "" {
		return "", errors.New("chưa đăng nhập Suno (thiếu token)")
	}
	st, err := os.Stat(o.FilePath)
	if err != nil {
		return "", fmt.Errorf("không đọc được file: %w", err)
	}
	if st.IsDir() {
		return "", errors.New("đường dẫn là thư mục, không phải file audio")
	}
	client := o.Client
	if client == nil {
		client = uploadClient
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(o.FilePath)), ".")
	if ext == "" {
		ext = "wav"
	}
	filename := filepath.Base(o.FilePath)

	r.Step("upload", 0.02, "Đang tạo phiên upload", filename)
	init, err := createUpload(ctx, client, o, ext, r)
	if err != nil {
		return "", err
	}

	r.Step("upload", 0.1, "Đang tải file lên Suno", filename)
	if err := putToS3(ctx, client, init, o.FilePath); err != nil {
		return "", err
	}

	r.Step("upload", 0.9, "Đang hoàn tất", filename)
	if err := finishUpload(ctx, client, o, init.ID, filename); err != nil {
		return "", err
	}

	// The clip only appears in the library once it is initialised; without this
	// the raw file lands in storage and is otherwise invisible.
	r.Step("upload", 0.96, "Đang tạo clip", filename)
	clipID, err := initializeClip(ctx, client, o, init.ID, r)
	if err != nil {
		return "", err
	}
	r.Step("upload", 1, "Upload xong", filename)
	return clipID, nil
}

// createUpload reserves the id and presigned POST.
func createUpload(ctx context.Context, client *http.Client, o UploadOptions, ext string, r Reporter) (uploadInit, error) {
	payload, _ := json.Marshal(map[string]string{
		"extension":   ext,
		"upload_type": "file_upload",
	})
	req, err := newStudioRequest(ctx, http.MethodPost, "/api/uploads/audio/", bytes.NewReader(payload), o)
	if err != nil {
		return uploadInit{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return uploadInit{}, fmt.Errorf("không gọi được Suno: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return uploadInit{}, apiError("tạo phiên upload", resp.StatusCode, body)
	}
	var init uploadInit
	if err := json.Unmarshal(body, &init); err != nil {
		return uploadInit{}, fmt.Errorf("không đọc được phản hồi Suno: %w", err)
	}
	if init.ID == "" || init.URL == "" {
		return uploadInit{}, errors.New("Suno không trả về thông tin upload hợp lệ")
	}
	return init, nil
}

// putToS3 sends the file through the presigned POST. S3 rejects a chunked body
// (HTTP 411 MissingContentLength), so the multipart payload is assembled into a
// temp file first — that gives an exact Content-Length without holding the whole
// upload in memory. `file` is written last because S3 requires the file field to
// follow every policy field.
func putToS3(ctx context.Context, client *http.Client, init uploadInit, filePath string) error {
	body, err := os.CreateTemp("", "suno-upload-*.multipart")
	if err != nil {
		return err
	}
	defer os.Remove(body.Name())
	defer body.Close()

	mw := multipart.NewWriter(body)
	for k, v := range init.Fields {
		if err := mw.WriteField(k, v); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return err
	}
	src, err := os.Open(filePath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, src); err != nil {
		src.Close()
		return err
	}
	src.Close()
	if err := mw.Close(); err != nil {
		return err
	}

	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return err
	}
	stat, err := body.Stat()
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, init.URL, body)
	if err != nil {
		return err
	}
	// Set explicitly: an *os.File body does not auto-populate Content-Length the
	// way a *bytes.Reader would, and its absence is exactly the 411 above.
	req.ContentLength = stat.Size()
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://suno.com")
	req.Header.Set("Referer", "https://suno.com/")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("không tải được file lên S3: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	// S3 presigned POST answers 200 or 204 on success.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return apiError("tải file lên S3", resp.StatusCode, respBody)
	}
	return nil
}

// finishUpload registers the landed file and names the clip.
func finishUpload(ctx context.Context, client *http.Client, o UploadOptions, id, filename string) error {
	payload, _ := json.Marshal(map[string]any{
		"upload_type":                "file_upload",
		"upload_filename":            filename,
		"agreed_to_vip_upload_terms": false,
	})
	req, err := newStudioRequest(ctx, http.MethodPost, "/api/uploads/audio/"+id+"/upload-finish/", bytes.NewReader(payload), o)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("không hoàn tất được upload: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return apiError("hoàn tất upload", resp.StatusCode, body)
	}
	return nil
}

// clipInitAttempts and clipInitDelay bound the wait for Suno to finish
// processing the just-uploaded file. initialize-clip answers 400 until the file
// is ready — the web client learns this by polling — so this retries rather than
// giving up on the first (expected) rejection. ~60 × 3s ≈ 3 phút covers even a
// long file; a genuinely stuck upload eventually surfaces the last error.
const (
	clipInitAttempts = 60
	clipInitDelay    = 3 * time.Second
)

// initializeClip promotes a finished upload into a library clip and returns its
// clip id. This is the step that makes the upload show up in the user's account.
//
// It polls: Suno rejects the call with 400 while the upload is still being
// processed, so a 400 is treated as "not ready yet" and retried. Auth failures,
// which waiting cannot fix, break out immediately.
func initializeClip(ctx context.Context, client *http.Client, o UploadOptions, id string, r Reporter) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= clipInitAttempts; attempt++ {
		clipID, status, err := tryInitClip(ctx, client, o, id)
		if err == nil {
			return clipID, nil
		}
		lastErr = err
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return "", err
		}
		if attempt < clipInitAttempts {
			r.Step("upload", 0.96, "Đang chờ Suno xử lý", fmt.Sprintf("thử lại (%d/%d)", attempt, clipInitAttempts))
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(clipInitDelay):
			}
		}
	}
	return "", fmt.Errorf("Suno chưa xử lý xong file sau khi chờ: %w", lastErr)
}

// tryInitClip makes one initialize-clip attempt. It returns the HTTP status
// alongside the error so the caller can tell a retryable "not ready" (400) from
// a terminal auth failure.
func tryInitClip(ctx context.Context, client *http.Client, o UploadOptions, id string) (string, int, error) {
	payload, _ := json.Marshal(map[string]any{"user_reviewed_tags": true})
	req, err := newStudioRequest(ctx, http.MethodPost, "/api/uploads/audio/"+id+"/initialize-clip/", bytes.NewReader(payload), o)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("không tạo được clip: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, apiError("tạo clip", resp.StatusCode, body)
	}
	var out struct {
		ClipID string `json:"clip_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", resp.StatusCode, fmt.Errorf("không đọc được clip_id từ Suno: %w", err)
	}
	if out.ClipID == "" {
		return "", resp.StatusCode, errors.New("Suno không trả về clip_id")
	}
	return out.ClipID, resp.StatusCode, nil
}

// newStudioRequest builds a request to studio-api carrying the three headers the
// web client authenticates with.
func newStudioRequest(ctx context.Context, method, path string, body io.Reader, o UploadOptions) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, studioAPIBase+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.Token)
	req.Header.Set("browser-token", browserToken(time.Now().UnixMilli()))
	if o.DeviceID != "" {
		req.Header.Set("device-id", o.DeviceID)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://suno.com")
	req.Header.Set("Referer", "https://suno.com/")
	return req, nil
}

// browserToken reproduces the anti-bot header the web client sends. Decoded, the
// value is only {"token": base64({"timestamp": <unix ms>})} — no secret in it —
// so it is regenerated fresh per request rather than captured.
func browserToken(nowMs int64) string {
	inner := fmt.Sprintf(`{"timestamp":%d}`, nowMs)
	enc := base64.StdEncoding.EncodeToString([]byte(inner))
	return fmt.Sprintf(`{"token":%q}`, enc)
}

// apiError turns a non-2xx response into a message with enough of the body to be
// useful without dumping a whole error page into the log.
func apiError(stage string, status int, body []byte) error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	if detail == "" || detail == "{}" {
		return fmt.Errorf("%s thất bại (HTTP %d)", stage, status)
	}
	return fmt.Errorf("%s thất bại (HTTP %d): %s", stage, status, detail)
}
