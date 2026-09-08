// Package ytdl drives yt-dlp to turn a YouTube URL into a local audio file.
package ytdl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"demucs-studio/internal/paths"
	"demucs-studio/internal/proc"
)

// Reporter is the subset of the event bus this package needs.
type Reporter interface {
	Log(level, text string)
	Logf(level, format string, args ...any)
	Step(phase string, fraction float64, label, detail string)
}

// Info is the metadata shown before the user commits to a download.
type Info struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Uploader   string  `json:"uploader"`
	Duration   float64 `json:"duration"`
	Thumbnail  string  `json:"thumbnail"`
	WebpageURL string  `json:"webpageUrl"`
	IsLive     bool    `json:"isLive"`
}

// Track is a downloaded audio file.
type Track struct {
	Path      string  `json:"path"`
	Title     string  `json:"title"`
	Format    string  `json:"format"`
	SizeBytes int64   `json:"sizeBytes"`
	Duration  float64 `json:"duration"`
	Info      Info    `json:"info"`
}

// Options configures a download.
type Options struct {
	YtDlp      string // path to the yt-dlp executable
	FfmpegDir  string // passed as --ffmpeg-location
	URL        string
	OutDir     string
	Format     string // wav | flac | mp3
	Mp3Bitrate int
	// CookiesFromBrowser forwards --cookies-from-browser, needed for
	// age-restricted videos and when YouTube demands a signed-in client.
	CookiesFromBrowser string
	// JSRuntime is yt-dlp's --js-runtimes value ("node:/usr/bin/node").
	// YouTube requires solving a JavaScript challenge to obtain playable media
	// URLs; without a runtime yt-dlp drops formats and downloads fail with 403.
	JSRuntime string
	// FilenameBase names the output file instead of yt-dlp's
	// "%(title)s [%(id)s]" template. Needed when URL is a direct CDN link
	// resolved elsewhere (Suno), where yt-dlp's own title is a bare UUID.
	FilenameBase string
	// Source names the site the URL came from, for progress labels. It also
	// gates the "yt-dlp is stale" advice, which is about YouTube's defences and
	// only misleads when the failure came from somewhere else. Empty means
	// YouTube.
	Source string
}

// SourceYouTube is the default value of Options.Source.
const SourceYouTube = "YouTube"

func (o Options) source() string {
	if s := strings.TrimSpace(o.Source); s != "" {
		return s
	}
	return SourceYouTube
}

// validateURL rejects anything that is not an http(s) URL.
//
// This is a security boundary, not a nicety. The URL is passed to yt-dlp as a
// positional argument, and yt-dlp's argument parser accepts any leading-dash
// value as an option — including `--exec`, which runs a shell command. Verified:
// passing "--version" here makes yt-dlp print its version instead of reporting
// a bad URL. Callers additionally pass "--" before the URL, so both the parser
// and this check have to be defeated for an option to slip through.
func validateURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("chưa nhập URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("URL không hợp lệ: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("chỉ nhận link http/https, không nhận %q", s)
	}
	if u.Host == "" {
		return "", errors.New("URL thiếu tên miền")
	}
	return s, nil
}

// sharedArgs are the flags every yt-dlp invocation needs.
func sharedArgs(o Options) []string {
	args := []string{"--no-playlist", "--no-warnings"}
	if o.JSRuntime != "" {
		args = append(args, "--js-runtimes", o.JSRuntime)
	}
	if b := strings.TrimSpace(o.CookiesFromBrowser); b != "" {
		args = append(args, "--cookies-from-browser", b)
	}
	return args
}

// staleSignatures are yt-dlp failure messages that in practice mean the local
// yt-dlp is older than YouTube's current defences, not that the link is bad.
// YouTube ships new anti-bot checks every few weeks, so this is the single most
// common cause of download failures.
var staleSignatures = []string{
	"http error 403",
	"po token",
	"needs to be reloaded",
	"sign in to confirm",
	"unable to download video data",
	"requested format is not available",
	"no supported javascript runtime",
}

// explainFailure turns an opaque yt-dlp exit into something the user can act on.
//
// The advice is YouTube-specific, so a download from anywhere else gets the raw
// error rather than a confident pointer at the wrong cause.
func explainFailure(runErr error, stderr []string, hasJSRuntime bool, source string) error {
	if source != "" && source != SourceYouTube {
		return runErr
	}
	joined := strings.ToLower(strings.Join(stderr, "\n"))
	matched := ""
	for _, sig := range staleSignatures {
		if strings.Contains(joined, sig) {
			matched = sig
			break
		}
	}
	if matched == "" {
		return runErr
	}
	advice := "YouTube vừa siết chặn tải; nguyên nhân phổ biến nhất là yt-dlp đã cũ. " +
		"Mở tab Phụ thuộc → Cập nhật yt-dlp rồi thử lại."
	if !hasJSRuntime {
		advice += " Ngoài ra máy chưa có JS runtime (deno/node/bun) — yt-dlp cần nó để " +
			"giải thử thách JavaScript của YouTube."
	}
	advice += " Nếu vẫn lỗi, chọn Cấu hình → Cookies từ browser đang đăng nhập YouTube."
	return fmt.Errorf("%w\n\n%s", runErr, advice)
}

// progress is the payload of our --progress-template line. Using an explicit
// template (rather than scraping yt-dlp's human-readable bar) keeps parsing
// stable across yt-dlp versions.
const progressPrefix = "DLPROG "

const progressTemplate = "download:" + progressPrefix +
	"%(progress._percent_str)s|%(progress._downloaded_bytes_str)s|" +
	"%(progress._total_bytes_estimate_str)s|%(progress._speed_str)s|%(progress._eta_str)s"

// infoTemplate makes yt-dlp emit exactly the metadata fields Info holds, as one
// JSON object. The ".{…}" subset selector keeps it to those fields and the "j"
// conversion serialises them, so the result parses with the same struct as
// --dump-single-json. Fields the extractor never set are simply absent.
const infoTemplate = "%(.{id,title,uploader,channel,duration,thumbnail,webpage_url,is_live})j"

// liveFilter is the --match-filter expression that refuses a live stream.
const liveFilter = "!is_live"

// liveSkipMarker is what yt-dlp prints when liveFilter rejects the URL. It exits
// 0 and downloads nothing, so without spotting this the caller would only see
// "yt-dlp did not report an output path" — true, but useless.
const liveSkipMarker = "does not pass filter (" + liveFilter + ")"

// FetchInfo resolves title/duration/thumbnail without downloading media.
func FetchInfo(ctx context.Context, o Options, r Reporter) (Info, error) {
	if o.YtDlp == "" {
		return Info{}, errors.New("chưa tìm thấy yt-dlp")
	}
	target, err := validateURL(o.URL)
	if err != nil {
		return Info{}, err
	}
	args := append(sharedArgs(o), "--dump-single-json", "--skip-download")
	// "--" ends option parsing, so a URL is never mistaken for a flag.
	args = append(args, "--", target)

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var stderr []string
	out, err := proc.Output(ctx, proc.Options{
		Bin:  o.YtDlp,
		Args: args,
		OnLine: func(stream, line string) {
			if stream == "stderr" {
				stderr = append(stderr, line)
				r.Log("warn", line)
			}
		},
	})
	if err != nil {
		wrapped := fmt.Errorf("không đọc được thông tin video: %w", err)
		return Info{}, explainFailure(wrapped, stderr, o.JSRuntime != "", o.source())
	}

	raw, err := parseInfo(out)
	if err != nil {
		return Info{}, err
	}
	if raw.Type == "playlist" {
		return Info{}, errors.New("URL này là playlist; hãy dùng link của một video cụ thể")
	}
	return raw.info(), nil
}

// rawInfo is the JSON both metadata paths produce: --dump-single-json here, and
// the --print-to-file field subset that Download writes during its own run.
type rawInfo struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Uploader   string  `json:"uploader"`
	Channel    string  `json:"channel"`
	Duration   float64 `json:"duration"`
	Thumbnail  string  `json:"thumbnail"`
	WebpageURL string  `json:"webpage_url"`
	IsLive     bool    `json:"is_live"`
	Type       string  `json:"_type"`
}

func (r rawInfo) info() Info {
	uploader := r.Uploader
	if uploader == "" {
		uploader = r.Channel
	}
	return Info{
		ID:         r.ID,
		Title:      r.Title,
		Uploader:   uploader,
		Duration:   r.Duration,
		Thumbnail:  r.Thumbnail,
		WebpageURL: r.WebpageURL,
		IsLive:     r.IsLive,
	}
}

func parseInfo(out string) (rawInfo, error) {
	var raw rawInfo
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &raw); err != nil {
		return rawInfo{}, fmt.Errorf("không phân tích được JSON của yt-dlp: %w", err)
	}
	return raw, nil
}

// Download fetches the best audio stream and transcodes it to o.Format.
func Download(ctx context.Context, o Options, r Reporter) (Track, error) {
	if o.YtDlp == "" {
		return Track{}, errors.New("chưa tìm thấy yt-dlp")
	}
	if o.FfmpegDir == "" {
		return Track{}, errors.New("chưa tìm thấy ffmpeg — cần ffmpeg để chuyển sang " + strings.ToUpper(o.Format))
	}
	target, err := validateURL(o.URL)
	if err != nil {
		return Track{}, err
	}
	if err := paths.EnsureDir(o.OutDir); err != nil {
		return Track{}, err
	}

	// yt-dlp knows the final path only after post-processing; have it write that
	// path to a file rather than trying to parse it out of the log.
	pathFile, err := os.CreateTemp("", "ytdlp-path-*.txt")
	if err != nil {
		return Track{}, err
	}
	pathFile.Close()
	defer os.Remove(pathFile.Name())

	// Metadata comes out of this same run. It used to take a second yt-dlp
	// process ahead of the download — measured at 3.2s against YouTube, ~40% of
	// the step — which re-fetched the page and re-solved the JS challenge only
	// to learn what this run extracts anyway.
	infoFile, err := os.CreateTemp("", "ytdlp-info-*.json")
	if err != nil {
		return Track{}, err
	}
	infoFile.Close()
	defer os.Remove(infoFile.Name())

	// "0" means best VBR quality; for MP3 an explicit bitrate is friendlier.
	quality := "0"
	if o.Format == "mp3" && o.Mp3Bitrate > 0 {
		quality = fmt.Sprintf("%dK", o.Mp3Bitrate)
	}

	args := append(sharedArgs(o),
		"--newline",
		"--no-mtime",
		"--progress",
		"--progress-template", progressTemplate,
		"-f", "bestaudio/best",
		"--extract-audio",
		"--audio-format", o.Format,
		"--audio-quality", quality,
		"--ffmpeg-location", o.FfmpegDir,
		// Normalise to 44.1 kHz stereo so both engines see consistent input.
		"--postprocessor-args", "ExtractAudio:-ac 2 -ar 44100",
		"-o", outputTemplate(o),
		"--print-to-file", "after_move:filepath", pathFile.Name(),
		"--print-to-file", "after_move:"+infoTemplate, infoFile.Name(),
		// The livestream guard. It used to be a check on the pre-flight
		// metadata; letting yt-dlp apply it keeps a live URL from downloading
		// forever without costing a second extraction. A source whose extractor
		// never sets is_live — the generic one, which is what a resolved CDN
		// link uses — passes, since "!field" also means "absent".
		"--match-filter", liveFilter,
		// Without this, re-running the same URL skips post-processing, so
		// nothing is written to the path file and we cannot tell the caller
		// where the audio ended up.
		"--force-overwrites",
	)
	// "--" ends option parsing, so a URL is never mistaken for a flag.
	args = append(args, "--", target)

	r.Logf("info", "yt-dlp %s", strings.Join(args, " "))
	r.Step("download", -1, "Đang kết nối "+o.source(), "")

	converting := false
	skippedLive := false
	var stderr []string
	runErr := proc.Run(ctx, proc.Options{
		Bin:         o.YtDlp,
		Args:        args,
		PrependPath: []string{o.FfmpegDir},
		OnLine: func(stream, line string) {
			if strings.Contains(line, liveSkipMarker) {
				skippedLive = true
			}
			if rest, ok := strings.CutPrefix(line, progressPrefix); ok {
				pct, detail := parseProgress(rest)
				// Downloading is ~85% of the perceived work; transcoding the
				// rest, so the bar does not sit at 100% during conversion.
				r.Step("download", pct*0.85, "Đang tải audio", detail)
				return
			}
			// Must match only the post-processor's line. yt-dlp prints
			// "[download] Destination: ...webm" before the first byte arrives,
			// so keying on "Destination:" alone fired this latch at 0% and left
			// the real transcode with no progress at all.
			if strings.Contains(line, "[ExtractAudio]") && !converting {
				converting = true
				r.Step("download", 0.9, "Đang chuyển sang "+strings.ToUpper(o.Format), "")
			}
			level := "info"
			if stream == "stderr" {
				level = "warn"
				stderr = append(stderr, line)
			}
			r.Log(level, line)
		},
	})
	if runErr != nil {
		return Track{}, explainFailure(runErr, stderr, o.JSRuntime != "", o.source())
	}
	if skippedLive {
		return Track{}, errors.New("đây là livestream đang phát; không thể tải thành file audio")
	}

	finalPath, err := readPathFile(pathFile.Name())
	if err != nil {
		return Track{}, err
	}
	st, err := os.Stat(finalPath)
	if err != nil {
		return Track{}, fmt.Errorf("không tìm thấy file vừa tải: %w", err)
	}

	r.Step("download", 1, "Tải xong", filepath.Base(finalPath))
	// The filename is the fallback title: it is always there, whereas metadata
	// is best-effort — a generic-extractor source may report almost nothing.
	title := strings.TrimSuffix(filepath.Base(finalPath), filepath.Ext(finalPath))
	info := readInfoFile(infoFile.Name(), r)
	// A caller that supplied FilenameBase already knows the real title, and
	// yt-dlp does not: on a direct CDN link the generic extractor derives
	// "title" from the URL basename, so letting it win here turned a resolved
	// Suno song back into its UUID.
	if info.Title != "" && o.FilenameBase == "" {
		title = info.Title
	}
	return Track{
		Path:      finalPath,
		Title:     title,
		Format:    strings.TrimPrefix(strings.ToLower(filepath.Ext(finalPath)), "."),
		SizeBytes: st.Size(),
		Duration:  info.Duration,
		Info:      info,
	}, nil
}

// readInfoFile reads the metadata yt-dlp printed during the download. Metadata
// is a nicety next to the audio file itself, so a failure here is logged and
// swallowed rather than throwing away a completed download.
func readInfoFile(name string, r Reporter) Info {
	body, err := os.ReadFile(name)
	if err != nil {
		r.Logf("warn", "Không đọc được metadata: %v", err)
		return Info{}
	}
	if strings.TrimSpace(string(body)) == "" {
		return Info{}
	}
	raw, err := parseInfo(string(body))
	if err != nil {
		r.Logf("warn", "%v", err)
		return Info{}
	}
	return raw.info()
}

// outputTemplate builds the -o value. yt-dlp's own title is the right name for
// a real extractor, but a direct CDN link only ever yields the filename, so
// callers that already know the title supply it as FilenameBase.
func outputTemplate(o Options) string {
	base := sanitizeFilename(o.FilenameBase)
	if base == "" {
		return filepath.Join(o.OutDir, "%(title).120B [%(id)s].%(ext)s")
	}
	return filepath.Join(o.OutDir, base+".%(ext)s")
}

// sanitizeFilename turns a title into a filename that is legal on every
// platform we ship to and inert as a yt-dlp output template.
func sanitizeFilename(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		// The Windows-reserved set, plus separators, plus control characters —
		// which a Suno title can carry, since its titles are free text.
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, s)
	// Trailing dots and spaces are silently dropped by Windows, which would
	// leave the extension welded onto the name.
	cleaned = strings.Trim(strings.TrimSpace(cleaned), " .")
	cleaned = truncateBytes(cleaned, 120)
	// Escape only after truncating, so the cut cannot land inside a "%%" pair
	// and resurrect a template field.
	return strings.ReplaceAll(cleaned, "%", "%%")
}

// truncateBytes cuts s to at most n bytes without splitting a rune, matching
// the "%(title).120B" limit used for the default template.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " .")
}

// readPathFile reads the final media path yt-dlp recorded. yt-dlp appends one
// line per downloaded item; we take the last non-empty one.
func readPathFile(name string) (string, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if p := strings.TrimSpace(lines[i]); p != "" {
			return p, nil
		}
	}
	return "", errors.New("yt-dlp không báo đường dẫn file kết quả")
}

// parseProgress turns "  42.3%|1.20MiB|2.80MiB|500KiB/s|00:03" into a fraction
// and a display string. yt-dlp writes "N/A" for unknown fields.
func parseProgress(s string) (float64, string) {
	parts := strings.Split(s, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	pct := -1.0
	if len(parts) > 0 {
		if v, err := parsePercent(parts[0]); err == nil {
			pct = v
		}
	}
	var detail []string
	if len(parts) > 2 && parts[1] != "" && parts[1] != "N/A" {
		if parts[2] != "" && parts[2] != "N/A" {
			detail = append(detail, parts[1]+" / "+parts[2])
		} else {
			detail = append(detail, parts[1])
		}
	}
	if len(parts) > 3 && parts[3] != "" && parts[3] != "N/A" {
		detail = append(detail, parts[3])
	}
	if len(parts) > 4 && parts[4] != "" && parts[4] != "N/A" {
		detail = append(detail, "còn "+parts[4])
	}
	return pct, strings.Join(detail, "  ·  ")
}

func parsePercent(s string) (float64, error) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%"))
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0, err
	}
	return v / 100, nil
}
