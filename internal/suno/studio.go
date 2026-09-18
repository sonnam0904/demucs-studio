// Studio-side API: turning an uploaded clip into a reversed clip via Suno's
// Studio "render-state" endpoint.
//
// The reverse itself is not an AI operation — it is a DAW flag (clip.reversed)
// plus a server render of the project state. This file reproduces that render
// without the LLM agent that the web client wraps around it: the agent only
// decides which tools to call, and for "reverse + save to library" those tools
// reduce to (1) set reversed=true and (2) render-state. Everything here is
// reverse-engineered from a real session and is inherently fragile: the Studio
// API is private/beta and can change without notice.
package suno

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

// reverseTemplate is a known-good render-state body captured from a real
// session. The reverse flow reuses it as a skeleton and overwrites only the
// clip-specific fields — safer than hand-building the ~14KB DAW state and risking
// a missing field.
//
//go:embed reverse_template.json
var reverseTemplate []byte

// StudioAuth carries the credentials every studio-api call needs.
type StudioAuth struct {
	Token    string
	DeviceID string
}

// RenderedClip is the outcome of a render-state call.
type RenderedClip struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	AudioURL  string `json:"audio_url"`
	MediaURLs []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"media_urls"`
	Metadata struct {
		Duration float64 `json:"duration"`
	} `json:"metadata"`
}

// PageURL is the human-facing suno.com page for the clip.
func (c RenderedClip) PageURL() string { return "https://suno.com/song/" + c.ID }

// PlayableURL returns the first real (non-forbidden) media URL, empty if none.
func (c RenderedClip) PlayableURL() string {
	for _, m := range c.MediaURLs {
		if m.URL != "" {
			return m.URL
		}
	}
	return ""
}

// GetClipStatus reads a clip's current render status.
func GetClipStatus(ctx context.Context, client *http.Client, clipID string, a StudioAuth) (RenderedClip, error) {
	if client == nil {
		client = uploadClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, studioAPIBase+"/api/clip/"+clipID, nil)
	if err != nil {
		return RenderedClip{}, err
	}
	setStudioHeaders(req, a)
	resp, err := client.Do(req)
	if err != nil {
		return RenderedClip{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return RenderedClip{}, apiError("đọc trạng thái clip", resp.StatusCode, body)
	}
	var c RenderedClip
	if err := json.Unmarshal(body, &c); err != nil {
		return RenderedClip{}, err
	}
	return c, nil
}

// PollRender waits for a rendered clip to finish, returning it once its status is
// terminal. "complete"/"streaming" succeed; "error"/"failed" return an error.
func PollRender(ctx context.Context, client *http.Client, clipID string, a StudioAuth, r Reporter) (RenderedClip, error) {
	const attempts = 60
	const delay = 3 * time.Second
	var last RenderedClip
	for i := 1; i <= attempts; i++ {
		c, err := GetClipStatus(ctx, client, clipID, a)
		if err == nil {
			last = c
			switch c.Status {
			case "complete", "streaming":
				return c, nil
			case "error", "failed":
				return c, fmt.Errorf("Suno render lỗi (status=%s)", c.Status)
			}
		}
		if r != nil {
			r.Step("suno-render", 0.5, "Đang render trên Suno", fmt.Sprintf("%s (%d/%d)", last.Status, i, attempts))
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(delay):
		}
	}
	return last, fmt.Errorf("hết thời gian chờ render (status=%s)", last.Status)
}

// setStudioHeaders applies the three auth headers plus the origin markers the
// web client sends. Mirrors newStudioRequest in upload.go but takes StudioAuth.
func setStudioHeaders(req *http.Request, a StudioAuth) {
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("browser-token", browserToken(time.Now().UnixMilli()))
	if a.DeviceID != "" {
		req.Header.Set("device-id", a.DeviceID)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", "https://suno.com")
	req.Header.Set("Referer", "https://suno.com/")
}

// RenderStateRaw POSTs a prebuilt render-state body and returns the rendered
// clip. Used both by the higher-level reverse flow and by the test harness that
// replays a captured body verbatim.
func RenderStateRaw(ctx context.Context, client *http.Client, body []byte, a StudioAuth) (RenderedClip, error) {
	if client == nil {
		client = uploadClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, studioAPIBase+"/api/studio/render-state", bytes.NewReader(body))
	if err != nil {
		return RenderedClip{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	setStudioHeaders(req, a)

	resp, err := client.Do(req)
	if err != nil {
		return RenderedClip{}, fmt.Errorf("không gọi được render-state: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return RenderedClip{}, apiError("render-state", resp.StatusCode, respBody)
	}
	var clip RenderedClip
	if err := json.Unmarshal(respBody, &clip); err != nil {
		return RenderedClip{}, fmt.Errorf("không đọc được phản hồi render-state: %w", err)
	}
	return clip, nil
}

// CreateProject makes a new, empty Studio project and returns its id. The clip
// is not attached here — the web client builds the track/arrangement state
// client-side and only sends it at render time, which is what ReverseClip does.
func CreateProject(ctx context.Context, client *http.Client, title string, a StudioAuth) (string, error) {
	if client == nil {
		client = uploadClient
	}
	u := studioAPIBase + "/api/studio/create-project?title=" + url.QueryEscape(title) + "&major_version=2"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return "", err
	}
	req.ContentLength = 0
	setStudioHeaders(req, a)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("không tạo được project: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", apiError("tạo project", resp.StatusCode, body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("không đọc được project id từ Suno")
	}
	return out.ID, nil
}

// ClipMeta is the handful of clip fields the reverse flow needs.
type ClipMeta struct {
	Duration float64
	Title    string
}

// GetClip reads a clip's duration and title, needed to size the reversed
// arrangement on the timeline.
func GetClip(ctx context.Context, client *http.Client, clipID string, a StudioAuth) (ClipMeta, error) {
	if client == nil {
		client = uploadClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, studioAPIBase+"/api/clip/"+clipID, nil)
	if err != nil {
		return ClipMeta{}, err
	}
	setStudioHeaders(req, a)

	resp, err := client.Do(req)
	if err != nil {
		return ClipMeta{}, fmt.Errorf("không đọc được clip: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return ClipMeta{}, apiError("đọc clip", resp.StatusCode, body)
	}
	var out struct {
		Title    string `json:"title"`
		Metadata struct {
			Duration float64 `json:"duration"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ClipMeta{}, fmt.Errorf("không đọc được metadata clip: %w", err)
	}
	return ClipMeta{Duration: out.Metadata.Duration, Title: out.Title}, nil
}

// ReverseClip runs the full "reverse an uploaded clip on Suno" flow: create an
// empty project, build a render-state that references the clip with reversed=true
// (warp off — a plain time-reverse needs no beat alignment), and render it into
// the library. Returns the rendered clip.
func ReverseClip(ctx context.Context, client *http.Client, clipID, title string, durationSec float64, keepWarp bool, a StudioAuth) (RenderedClip, error) {
	if durationSec <= 0 || title == "" {
		meta, err := GetClip(ctx, client, clipID, a)
		if err != nil {
			// keepWarp does not need the duration (it reuses the template's
			// beats), so a metadata fetch failure is not fatal there.
			if !keepWarp {
				return RenderedClip{}, err
			}
		} else {
			if durationSec <= 0 {
				durationSec = meta.Duration
			}
			if title == "" {
				title = meta.Title
			}
		}
	}
	if !keepWarp && durationSec <= 0 {
		return RenderedClip{}, fmt.Errorf("không xác định được độ dài clip")
	}
	if title == "" {
		title = "clip"
	}

	projectID, err := CreateProject(ctx, client, title, a)
	if err != nil {
		return RenderedClip{}, err
	}
	body, err := buildReverseBody(projectID, clipID, title, durationSec, keepWarp)
	if err != nil {
		return RenderedClip{}, err
	}
	rendered, err := RenderStateRaw(ctx, client, body, a)
	if err != nil {
		return RenderedClip{}, err
	}
	// Persist the project so the rendered clip lands in the library's Songs list
	// rather than only in History — the web client always saves around a render.
	if doc := decodeState(body); doc != nil {
		if err := SaveProject(ctx, client, projectID, doc, title, a); err != nil {
			return rendered, fmt.Errorf("render xong nhưng lưu project lỗi: %w", err)
		}
	}
	return rendered, nil
}

// decodeState pulls the "state" object back out of a render-state body so it can
// be reused for save-project.
func decodeState(body []byte) map[string]any {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		return nil
	}
	st, _ := doc["state"].(map[string]any)
	return st
}

// SaveProject persists a project's state. Called after a render so the result is
// registered in the user's library, not left as a History-only context render.
func SaveProject(ctx context.Context, client *http.Client, projectID string, state map[string]any, title string, a StudioAuth) error {
	if client == nil {
		client = uploadClient
	}
	payload, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"state":      state,
		"title":      title,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, studioAPIBase+"/api/studio/save-project", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	setStudioHeaders(req, a)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("không lưu được project: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return apiError("lưu project", resp.StatusCode, respBody)
	}
	return nil
}

// buildReverseBody clones the captured render-state skeleton and overwrites the
// clip-specific fields. With keepWarp=true it leaves the template's warp markers,
// beats and downbeats untouched (only valid when clipID is the SAME audio the
// template was captured from) — used to confirm that warp-on + reversed actually
// reverses. With keepWarp=false it disables warp and rebuilds beats for a generic
// clip (which, as found, does not actually reverse — Suno needs the warp path).
func buildReverseBody(projectID, clipID, title string, durationSec float64, keepWarp bool) ([]byte, error) {
	var body map[string]any
	if err := json.Unmarshal(reverseTemplate, &body); err != nil {
		return nil, fmt.Errorf("template render-state hỏng: %w", err)
	}
	state, _ := body["state"].(map[string]any)
	if state == nil {
		return nil, fmt.Errorf("template thiếu state")
	}
	bps := 1.5
	if t, ok := state["timing"].(map[string]any); ok {
		if v, ok := t["bps"].(float64); ok && v > 0 {
			bps = v
		}
	}
	endBeats := durationSec * bps
	fullTitle := title + " — reversed"

	body["title"] = fullTitle
	body["from_studio_project_id"] = projectID
	state["title"] = title

	tracks, _ := state["tracks"].([]any)
	if len(tracks) == 0 {
		return nil, fmt.Errorf("template thiếu track")
	}
	tr, _ := tracks[0].(map[string]any)
	tr["name"] = title
	clips, _ := tr["clips"].([]any)
	if len(clips) == 0 {
		return nil, fmt.Errorf("template thiếu clip")
	}
	c, _ := clips[0].(map[string]any)
	c["clipId"] = clipID
	c["asset"] = map[string]any{"type": "clip", "id": clipID}
	c["reversed"] = true
	c["name"] = title

	if keepWarp {
		// Same-audio confirmation: keep the template's warp markers, beats and
		// downbeats verbatim (valid only because clipID is the audio the template
		// was captured from). Only the clip/project identity changes above.
		return json.Marshal(body)
	}

	// Generic clip: no valid warp markers for this audio, so disable warp and lay
	// a plain beat grid sized to the clip.
	body["start_beats"] = 0.0
	body["end_beats"] = endBeats
	body["downbeats"] = gridDownbeats(endBeats, bps, 4)
	state["markersRegistry"] = map[string]any{}
	c["startBeats"] = 0.0
	c["endBeats"] = endBeats
	c["readStartBeats"] = 0.0
	c["loop"] = map[string]any{"enabled": false, "startBeats": 0.0, "endBeats": endBeats}
	c["warp"] = map[string]any{"enabled": false, "awaitingAnalysis": false, "markersHash": nil}
	if sel, ok := state["selection"].(map[string]any); ok {
		sel["anchorBeats"] = 0.0
		sel["focusBeats"] = endBeats
	}
	return json.Marshal(body)
}

// gridDownbeats lays a plain even beat grid over [0, endBeats]. With warp off the
// exact grid does not affect the rendered audio, but render-state still expects
// the field populated.
func gridDownbeats(endBeats, bps float64, beatsPerBar int) [][2]float64 {
	if bps <= 0 {
		bps = 1.5
	}
	if beatsPerBar <= 0 {
		beatsPerBar = 4
	}
	secPerBeat := 1.0 / bps
	n := int(math.Floor(endBeats))
	if n < 0 {
		n = 0
	}
	out := make([][2]float64, n)
	for i := 0; i < n; i++ {
		out[i] = [2]float64{float64(i) * secPerBeat, float64(i%beatsPerBar + 1)}
	}
	return out
}

// downbeats builds the [timeSeconds, beatInBar] grid render-state expects, from
// the project's tempo and clip range. Verified to reproduce a captured session's
// grid exactly:
//
//	t0            = (-startBeats - 1) / bps
//	downbeat[i]   = [ t0 + i/bps , ((i-1) mod beatsPerBar) + 1 ]
//	count         = floor(endBeats - startBeats)
func downbeats(startBeats, endBeats, bps float64, beatsPerBar int) [][2]float64 {
	if bps <= 0 {
		bps = 1
	}
	if beatsPerBar <= 0 {
		beatsPerBar = 4
	}
	secPerBeat := 1.0 / bps
	t0 := (-startBeats - 1) * secPerBeat
	n := int(math.Floor(endBeats - startBeats))
	if n < 0 {
		n = 0
	}
	out := make([][2]float64, n)
	for i := 0; i < n; i++ {
		label := ((i-1)%beatsPerBar + beatsPerBar) % beatsPerBar // 0-based, handles i=0 → beatsPerBar-1
		out[i] = [2]float64{t0 + float64(i)*secPerBeat, float64(label + 1)}
	}
	return out
}
