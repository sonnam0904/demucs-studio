// Package suno resolves a suno.com share link to a direct media URL that
// yt-dlp can download.
//
// yt-dlp refuses suno.com outright ("[Liability] This website is not supported
// and will not be supported"), so the link never reaches an extractor. The song
// page is a Next.js app, though, and it embeds the clip's own JSON — title,
// artist, duration, artwork and media URLs — in its flight payload. Reading
// that gives us a plain CDN URL, which yt-dlp's generic extractor handles like
// any other direct media file.
package suno

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Song is the metadata a share page yields, plus the URL to fetch the audio from.
type Song struct {
	ID         string
	Title      string
	Uploader   string
	Duration   float64
	Thumbnail  string
	WebpageURL string
	// MediaURL is a direct CDN link; it is what gets handed to yt-dlp.
	MediaURL string
}

// IsURL reports whether raw points at Suno. Callers use it to pick this
// resolver over the normal yt-dlp path.
func IsURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, domain := range []string{"suno.com", "suno.ai"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

// A stock browser UA. Suno serves the page to anything, but its edge has
// blocked unusual clients before and the payload we need is the one a browser
// receives.
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// maxPage caps how much of the page we buffer. Song pages run ~150 KB; the
// limit only exists so a redirect to something huge cannot exhaust memory.
const maxPage = 8 << 20

var client = &http.Client{Timeout: 60 * time.Second}

// Resolve fetches the share page and extracts the song's metadata and media URL.
func Resolve(ctx context.Context, raw string) (Song, error) {
	page, finalURL, err := fetchPage(ctx, raw)
	if err != nil {
		return Song{}, err
	}
	c, err := extractClip(page)
	if err != nil {
		return Song{}, err
	}
	media := c.mediaURL(ctx)
	if media == "" {
		return Song{}, c.unavailableErr()
	}
	return Song{
		ID:         c.ID,
		Title:      strings.TrimSpace(c.Title),
		Uploader:   c.uploader(),
		Duration:   c.Metadata.Duration,
		Thumbnail:  firstNonEmpty(c.ImageLargeURL, c.ImageURL),
		WebpageURL: finalURL,
		MediaURL:   media,
	}, nil
}

func fetchPage(ctx context.Context, raw string) (page, finalURL string, err error) {
	target := strings.TrimSpace(raw)
	if !IsURL(target) {
		return "", "", fmt.Errorf("không phải link Suno: %q", raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("không mở được trang Suno: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Suno trả về %s cho %s", resp.Status, target)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPage))
	if err != nil {
		return "", "", fmt.Errorf("không đọc được trang Suno: %w", err)
	}
	final := target
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return string(body), final, nil
}

// clip mirrors the fields we need from the page's embedded clip object.
type clip struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	DisplayName   string `json:"display_name"`
	Handle        string `json:"handle"`
	ImageURL      string `json:"image_url"`
	ImageLargeURL string `json:"image_large_url"`
	AudioURL      string `json:"audio_url"`
	VideoURL      string `json:"video_url"`
	// IsPublic is a pointer so that "field absent" stays distinguishable from
	// "explicitly false" — only the latter justifies blaming the song's privacy.
	IsPublic  *bool `json:"is_public"`
	MediaURLs []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"media_urls"`
	Metadata struct {
		Duration float64 `json:"duration"`
	} `json:"metadata"`
}

func (c clip) uploader() string {
	if name := strings.TrimSpace(c.DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(c.Handle)
}

// mediaURL picks the stream to download.
//
// Order matters. audio_url is the real audio when the song is public, but for a
// signed-out visitor Suno substitutes a placeholder pointing at its own
// /api/forbidden, so it has to be sanity-checked. video_url is next: it is an
// ordinary MP4 on cdn1.suno.ai whose audio track is the full song, and Suno only
// generates it for a public song. media_urls comes last and is probed rather
// than trusted — see looksLikeMedia.
func (c clip) mediaURL(ctx context.Context) string {
	if u := usableMedia(c.AudioURL); u != "" {
		return u
	}
	if u := usableMedia(c.VideoURL); u != "" {
		return u
	}
	for _, m := range c.MediaURLs {
		if u := usableMedia(m.URL); u != "" && looksLikeMedia(ctx, u) {
			return u
		}
	}
	return ""
}

// unavailableErr explains why nothing was downloadable.
//
// Careful with the wording: is_public=false does NOT mean the page is
// unreachable. A song in that state still opens for anyone holding the link and
// still plays, because the player streams media_urls — so telling the user the
// song is "private" contradicts what they can see with their own browser. What
// is actually missing is a plain file: Suno populates video_url only for a
// public song, and everything else it offers is the encrypted player stream.
// Both variants end in the same way out, so it is written once — lowercase, to
// read naturally after either lead-in.
const manualFallback = "bấm Download trên Suno rồi chọn “…hoặc chọn file audio có sẵn” trong app"

func (c clip) unavailableErr() error {
	// A pointer, so an absent field is not read as "not public".
	if c.IsPublic != nil && !*c.IsPublic {
		return errors.New("Bài chưa ở chế độ Public nên Suno không sinh file tải được. " +
			"Chuyển sang Public rồi thử lại, hoặc " + manualFallback)
	}
	return errors.New("Suno chỉ phát bài này qua trình phát, không có file tải được — " +
		manualFallback)
}

// looksLikeMedia reports whether a URL actually serves a media container.
//
// Suno's media_urls entries are its player's encrypted stream, not a file: they
// answer 200 with Content-Type audio/mp4 and an honest Content-Length, but the
// bytes are not a container — measured on two songs, one public and one private,
// the payload starts with random data and ffprobe reports "moov atom not found".
// Handed to yt-dlp, that downloads several megabytes and then dies in
// post-processing with "unable to obtain file audio codec", which tells the user
// nothing. One ranged request for the first bytes settles it up front.
//
// Probed rather than dropped outright: if Suno ever serves a plain file here,
// this keeps using it.
func looksLikeMedia(ctx context.Context, raw string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", "bytes=0-15")
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return false
	}
	head, err := io.ReadAll(io.LimitReader(resp.Body, 16))
	if err != nil {
		return false
	}
	return isContainer(head)
}

// isContainer matches the magic bytes of the containers a Suno media URL could
// plausibly hold. Anything else is not something ffmpeg can open.
func isContainer(b []byte) bool {
	// ISO base media (mp4/m4a): a "ftyp" box, whose type sits after the size.
	if len(b) >= 8 && string(b[4:8]) == "ftyp" {
		return true
	}
	for _, magic := range []string{"ID3", "OggS", "RIFF", "fLaC"} {
		if strings.HasPrefix(string(b), magic) {
			return true
		}
	}
	// A bare MPEG audio frame: 11 sync bits.
	return len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0
}

func usableMedia(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	// Suno's stand-in for media the caller is not allowed to have.
	if strings.Contains(strings.ToLower(u.Path), "forbidden") {
		return ""
	}
	return s
}

// clipMarker is where the song object starts in the flight payload.
const clipMarker = `"clip":{`

func extractClip(page string) (clip, error) {
	flat := unescapeJS(page)
	i := strings.Index(flat, clipMarker)
	if i < 0 {
		return clip{}, errors.New("không tìm thấy dữ liệu bài hát trong trang Suno " +
			"(có thể trang đã đổi cấu trúc hoặc bài hát không công khai)")
	}
	obj, ok := objectAt(flat, i+len(clipMarker)-1)
	if !ok {
		return clip{}, errors.New("dữ liệu bài hát trong trang Suno bị cắt cụt")
	}
	var c clip
	if err := json.Unmarshal([]byte(obj), &c); err != nil {
		return clip{}, fmt.Errorf("không đọc được dữ liệu bài hát của Suno: %w", err)
	}
	return c, nil
}

// unescapeJS undoes one level of string escaping, the level Next.js adds when
// it embeds its JSON payload inside a JavaScript string literal.
//
// Only \\ and \" are collapsed. Every other escape is left byte-for-byte alone
// on purpose: inside the payload a JSON escape such as \n arrives as \\n, which
// this turns into \n — exactly what json.Unmarshal wants. Translating it
// further to a real newline would put a raw control character inside a JSON
// string and break the parse.
func unescapeJS(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '\\', '"':
				b.WriteByte(s[i+1])
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// objectAt returns the JSON object beginning at the '{' at index start, found
// by brace matching. Quotes and escapes inside strings are honoured so that a
// brace in a title or lyric does not end the object early.
func objectAt(s string, start int) (string, bool) {
	if start < 0 || start >= len(s) || s[start] != '{' {
		return "", false
	}
	depth := 0
	inString := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			switch ch {
			case '\\':
				i++ // skip the escaped byte, whatever it is
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
