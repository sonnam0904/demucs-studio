package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"demucs-studio/internal/bus"
	"demucs-studio/internal/deps"
	"demucs-studio/internal/engine"
	"demucs-studio/internal/engine/demucs"
	"demucs-studio/internal/engine/roformer"
	"demucs-studio/internal/paths"
	"demucs-studio/internal/settings"
	"demucs-studio/internal/ytdl"
)

// App is the object bound into the webview; every exported method is callable
// from TypeScript.
type App struct {
	ctx      context.Context
	bus      *bus.Bus
	settings *settings.Store
	resolver *deps.Resolver
	media    *mediaServer

	demucs   *demucs.Backend
	roformer *roformer.Backend

	// One long-running job at a time: the UI has a single progress bar and the
	// separation is CPU/GPU-bound anyway.
	jobMu     sync.Mutex
	jobCancel context.CancelFunc
	jobPhase  string

	// The window starts hidden and is revealed by whichever of the two paths in
	// reveal() gets there first.
	showOnce sync.Once
}

func NewApp(m *mediaServer) *App {
	b := bus.New()
	store := settings.Load()
	resolver := deps.NewResolver(store.Get)

	a := &App{
		bus:      b,
		settings: store,
		resolver: resolver,
		media:    m,
	}
	// Engines resolve their executables lazily so an install performed while
	// the app is open takes effect without a restart.
	a.demucs = demucs.New(func(ctx context.Context) []string {
		return resolver.Demucs(ctx).Argv
	})
	a.roformer = roformer.New(func(ctx context.Context) []string {
		return resolver.AudioSeparator(ctx).Argv
	})
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.bus.Attach(ctx)
	a.bus.Logf(bus.LevelInfo, "DemucsStudio khởi động — dữ liệu tại %s", paths.AppDir())

	// The audio previews need this; see the mediaServer doc comment for why a
	// loopback listener rather than the asset server.
	if err := a.media.Start(); err != nil {
		a.bus.Logf(bus.LevelError, "Không phát được audio trong app: %v", err)
	}

	// Warm the dependency and GPU probes off the UI thread; importing torch
	// takes several seconds and must not block the first paint.
	go func() {
		report := a.resolver.Report(ctx, true)
		for _, t := range report.Tools {
			if t.Found {
				a.bus.Logf(bus.LevelInfo, "%s: %s (%s)", t.Label, orDash(t.Version), t.Path)
			} else if t.Required {
				a.bus.Logf(bus.LevelWarn, "Thiếu %s — %s", t.Label, t.Hint)
			}
		}
		if report.GPU.Available {
			a.bus.Logf(bus.LevelInfo, "GPU khả dụng: %s (torch %s)", report.GPU.Name, report.GPU.Torch)
		} else if report.GPU.Checked {
			a.bus.Logf(bus.LevelWarn, "Sẽ tách bằng CPU (chậm hơn nhiều) — %s", orDash(report.GPU.Reason))
		}
		wruntime.EventsEmit(ctx, "app:deps", report)
	}()

	// A window that never appears is a worse failure than a brief flash of
	// unstyled HTML, so do not rely on OnDomReady being reached.
	go func() {
		select {
		case <-time.After(5 * time.Second):
			a.reveal(ctx)
		case <-ctx.Done():
		}
	}()
}

// domReady reveals the window once the DOM has parsed.
//
// The window is created hidden (StartHidden in main.go). Wails would otherwise
// map it before the webview has fetched anything: the asset server answers
// wails:// requests asynchronously, so the first paint is bare HTML and the
// stylesheet lands visibly later. That flash is seconds long on the software
// renderer WebKitGTK falls back to once the DMA-BUF path is disabled for the
// NVIDIA driver — see tuneWebKit.
//
// DOMContentLoaded is the right moment: a render-blocking <link rel=stylesheet>
// in <head> has already been applied by the time it fires.
func (a *App) domReady(ctx context.Context) {
	a.reveal(ctx)
}

func (a *App) reveal(ctx context.Context) {
	a.showOnce.Do(func() { wruntime.WindowShow(ctx) })
}

// --- payloads ---------------------------------------------------------------

// Bootstrap is everything the UI needs for its first render.
type Bootstrap struct {
	Settings   settings.Settings `json:"settings"`
	Deps       deps.Report       `json:"deps"`
	Models     []engine.Model    `json:"models"`
	Log        []bus.LogLine     `json:"log"`
	Platform   string            `json:"platform"`
	AppDir     string            `json:"appDir"`
	OutputDir  string            `json:"outputDir"`
	CPUs       int               `json:"cpus"`
	AppVersion string            `json:"appVersion"`
}

func (a *App) Bootstrap() Bootstrap {
	ctx := a.context()
	return Bootstrap{
		Settings:   a.settings.Get(),
		Deps:       a.resolver.Report(ctx, false),
		Models:     a.ListModels(),
		Log:        a.bus.History(),
		Platform:   runtime.GOOS + "/" + runtime.GOARCH,
		AppDir:     paths.AppDir(),
		OutputDir:  a.settings.Get().OutputDir,
		CPUs:       runtime.NumCPU(),
		AppVersion: appVersion,
	}
}

func (a *App) GetSettings() settings.Settings { return a.settings.Get() }

func (a *App) SaveSettings(next settings.Settings) (settings.Settings, error) {
	before := a.settings.Get()
	saved, err := a.settings.Set(next)
	if err != nil {
		a.bus.Logf(bus.LevelError, "Không lưu được cấu hình: %v", err)
		return saved, err
	}
	// Only re-probe when an executable override actually changed. The frontend
	// autosaves on every control, so invalidating unconditionally meant moving a
	// slider threw away the whole tool cache and forced a multi-second re-probe.
	if toolPathsDiffer(before, saved) {
		a.resolver.Invalidate()
		a.bus.Log(bus.LevelInfo, "Đường dẫn công cụ đã đổi — sẽ dò tìm lại.")
	}
	a.bus.Log(bus.LevelInfo, "Đã lưu cấu hình.")
	return saved, nil
}

// toolPathsDiffer reports whether any manual executable override changed.
func toolPathsDiffer(a, b settings.Settings) bool {
	return a.YtDlpPath != b.YtDlpPath ||
		a.FfmpegPath != b.FfmpegPath ||
		a.PythonPath != b.PythonPath ||
		a.DemucsPath != b.DemucsPath ||
		a.AudioSeparatorPath != b.AudioSeparatorPath
}

// ListModels returns both engines' models, recommended ones first.
func (a *App) ListModels() []engine.Model {
	ctx := a.context()
	all := append(a.demucs.Models(ctx), a.roformer.Models(ctx)...)
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Recommended != all[j].Recommended {
			return all[i].Recommended
		}
		return false
	})
	return all
}

// CheckDeps re-runs detection. checkGPU also forgets and repeats the GPU probe;
// without it the cached GPU verdict is preserved, because re-detecting yt-dlp
// says nothing about CUDA.
func (a *App) CheckDeps(checkGPU bool) deps.Report {
	if checkGPU {
		a.resolver.InvalidateAll()
	} else {
		a.resolver.Invalidate()
	}
	return a.resolver.Report(a.context(), checkGPU)
}

// --- installers -------------------------------------------------------------

func (a *App) InstallTool(id string) error {
	return a.runJob("install", func(ctx context.Context) error {
		rep := a.reporter()
		var err error
		switch id {
		case deps.ToolYtDlp:
			err = deps.InstallYtDlp(ctx, rep)
		case deps.ToolFFmpeg:
			err = deps.InstallFFmpeg(ctx, rep)
		default:
			err = fmt.Errorf("không hỗ trợ tự cài %q", id)
		}
		if err == nil {
			a.resolver.Invalidate()
		}
		return err
	})
}

func (a *App) InstallEngines(spec deps.EngineSpec) error {
	return a.runJob("install", func(ctx context.Context) error {
		return deps.InstallEngines(ctx, a.resolver, spec, a.reporter())
	})
}

func (a *App) UpdateYtDlp() error {
	return a.runJob("install", func(ctx context.Context) error {
		return deps.UpdateYtDlp(ctx, a.resolver, a.reporter())
	})
}

// SuggestedAccel tells the UI which torch flavour to preselect: reuse an
// existing torch when the host Python already has one, else GPU if an NVIDIA
// driver looks present, else CPU.
func (a *App) SuggestedAccel() string {
	ctx := a.context()
	if g := a.resolver.DetectGPU(ctx); g.Checked && g.Torch != "" {
		return "reuse"
	}
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		return "cuda"
	}
	return "cpu"
}

// --- YouTube ---------------------------------------------------------------

func (a *App) FetchInfo(url string) (ytdl.Info, error) {
	ctx := a.context()
	tool := a.resolver.YtDlp(ctx)
	if !tool.Found {
		return ytdl.Info{}, errors.New("chưa có yt-dlp. Mở tab Phụ thuộc để cài")
	}
	set := a.settings.Get()
	a.bus.Progress(bus.Progress{Phase: "info", Percent: -1, Label: "Đang đọc thông tin video"})
	defer a.bus.Idle()

	info, err := ytdl.FetchInfo(ctx, ytdl.Options{
		YtDlp:              tool.Path,
		URL:                url,
		CookiesFromBrowser: set.CookiesFromBrowser,
		JSRuntime:          a.resolver.JSRuntimeSpec(ctx),
	}, a.reporter())
	if err != nil {
		a.bus.Logf(bus.LevelError, "%v", err)
		return ytdl.Info{}, err
	}
	a.bus.Logf(bus.LevelInfo, "%s — %s (%s)", info.Title, info.Uploader, formatDuration(info.Duration))
	return info, nil
}

// Download fetches the URL's audio and returns the local file.
func (a *App) Download(url string) (ytdl.Track, error) {
	var track ytdl.Track
	err := a.runJob("download", func(ctx context.Context) error {
		tool := a.resolver.YtDlp(ctx)
		if !tool.Found {
			return errors.New("chưa có yt-dlp. Mở tab Phụ thuộc để cài")
		}
		ffmpegDir := a.resolver.FFmpegDir(ctx)
		set := a.settings.Get()

		// Best-effort metadata first, so the card can show a title even if the
		// filename gets mangled.
		jsRuntime := a.resolver.JSRuntimeSpec(ctx)
		info, infoErr := ytdl.FetchInfo(ctx, ytdl.Options{
			YtDlp:              tool.Path,
			URL:                url,
			CookiesFromBrowser: set.CookiesFromBrowser,
			JSRuntime:          jsRuntime,
		}, a.reporter())
		if infoErr != nil {
			a.bus.Logf(bus.LevelWarn, "Không đọc được metadata: %v", infoErr)
		} else if info.IsLive {
			return errors.New("đây là livestream đang phát; không thể tải thành file audio")
		}

		outDir := filepath.Join(set.OutputDir, "downloads")
		got, err := ytdl.Download(ctx, ytdl.Options{
			YtDlp:              tool.Path,
			FfmpegDir:          ffmpegDir,
			URL:                url,
			OutDir:             outDir,
			Format:             set.AudioFormat,
			Mp3Bitrate:         set.Mp3Bitrate,
			CookiesFromBrowser: set.CookiesFromBrowser,
			JSRuntime:          jsRuntime,
		}, a.reporter())
		if err != nil {
			return err
		}
		got.Info = info
		got.Duration = info.Duration
		if info.Title != "" {
			got.Title = info.Title
		}
		track = got
		a.media.Allow(got.Path)
		a.bus.Logf(bus.LevelInfo, "Đã lưu %s (%s)", got.Path, humanBytes(got.SizeBytes))
		return nil
	})
	return track, err
}

// --- separation -------------------------------------------------------------

func (a *App) EnsureModel(modelID string) error {
	return a.runJob("model", func(ctx context.Context) error {
		backend, model, err := a.lookupModel(ctx, modelID)
		if err != nil {
			return err
		}
		return backend.EnsureModel(ctx, model, a.reporter())
	})
}

// RefreshRoformerModels re-reads the model list from audio-separator.
func (a *App) RefreshRoformerModels() (int, error) {
	var n int
	err := a.runJob("model", func(ctx context.Context) error {
		got, err := a.roformer.Refresh(ctx, a.reporter())
		n = got
		return err
	})
	return n, err
}

// Separate downloads the model if needed, then runs it over inputPath.
func (a *App) Separate(inputPath, modelID string) (engine.Result, error) {
	var result engine.Result
	err := a.runJob("separate", func(ctx context.Context) error {
		if strings.TrimSpace(inputPath) == "" {
			return errors.New("chưa có file audio để tách")
		}
		if _, err := os.Stat(inputPath); err != nil {
			return fmt.Errorf("không đọc được file audio: %w", err)
		}
		backend, model, err := a.lookupModel(ctx, modelID)
		if err != nil {
			return err
		}
		rep := a.reporter()
		if err := backend.EnsureModel(ctx, model, rep); err != nil {
			return err
		}

		set := a.settings.Get()
		jobDir := filepath.Join(set.OutputDir, "stems", safeName(baseNameNoExt(inputPath)))
		if err := paths.EnsureDir(jobDir); err != nil {
			return err
		}

		// Always resolve to an explicit device rather than letting the engine
		// decide: both engines default to CUDA whenever torch reports it
		// available, which is exactly the case that crashes on GPUs the
		// installed torch has no kernels for.
		gpu := a.resolver.DetectGPU(ctx)
		device := set.Device
		switch device {
		case "auto":
			if gpu.Available {
				device = "cuda"
			} else {
				device = "cpu"
				if gpu.Reason != "" {
					a.bus.Logf(bus.LevelInfo, "Dùng CPU: %s", gpu.Reason)
				}
			}
		case "cuda":
			if !gpu.Available {
				a.bus.Logf(bus.LevelWarn, "Đã chọn GPU nhưng không dùng được (%s) — chuyển sang CPU.",
					orDash(gpu.Reason))
				device = "cpu"
			}
		}

		got, err := backend.Separate(ctx, engine.Request{
			Input:               inputPath,
			OutDir:              jobDir,
			Model:               model,
			Device:              device,
			Format:              set.StemFormat,
			Mp3Rate:             set.Mp3Bitrate,
			TwoStems:            set.TwoStems,
			Shifts:              set.Shifts,
			Overlap:             set.Overlap,
			Segment:             set.Segment,
			Jobs:                set.Jobs,
			RoformerSegmentSize: set.RoformerSegmentSize,
			RoformerOverlap:     set.RoformerOverlap,
			RoformerBatchSize:   set.RoformerBatchSize,
			Normalization:       set.Normalization,
		}, rep)
		if err != nil {
			return err
		}
		for _, s := range got.Stems {
			a.media.Allow(s.Path)
			a.bus.Logf(bus.LevelInfo, "%s → %s (%s)", s.Name, s.Path, humanBytes(s.SizeBytes))
		}
		a.bus.Logf(bus.LevelInfo, "Tách xong trong %s", formatDuration(got.Seconds))
		result = got
		return nil
	})
	return result, err
}

// Cancel aborts the running job, if any.
func (a *App) Cancel() {
	a.jobMu.Lock()
	cancel, phase := a.jobCancel, a.jobPhase
	a.jobMu.Unlock()
	if cancel == nil {
		return
	}
	a.bus.Logf(bus.LevelWarn, "Đang huỷ tác vụ %s…", phase)
	cancel()
}

func (a *App) IsBusy() bool {
	a.jobMu.Lock()
	defer a.jobMu.Unlock()
	return a.jobCancel != nil
}

// --- files & dialogs --------------------------------------------------------

// PickAudioFile lets the user separate a file they already have.
func (a *App) PickAudioFile() (string, error) {
	p, err := wruntime.OpenFileDialog(a.context(), wruntime.OpenDialogOptions{
		Title: "Chọn file audio",
		Filters: []wruntime.FileFilter{
			{DisplayName: "Audio", Pattern: "*.wav;*.mp3;*.flac;*.m4a;*.ogg;*.opus;*.aac;*.wma"},
			{DisplayName: "Tất cả", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	if p != "" {
		a.media.Allow(p)
	}
	return p, nil
}

func (a *App) PickOutputDir() (string, error) {
	return wruntime.OpenDirectoryDialog(a.context(), wruntime.OpenDialogOptions{
		Title: "Chọn thư mục lưu kết quả",
	})
}

// OpenPath opens a file or folder in the OS file manager / default app.
func (a *App) OpenPath(target string) error {
	if target == "" {
		return errors.New("đường dẫn trống")
	}
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("không tồn tại: %s", target)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.Command("open", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// RevealPath opens the folder containing target.
func (a *App) RevealPath(target string) error {
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		return a.OpenPath(target)
	}
	return a.OpenPath(filepath.Dir(target))
}

// MediaURL returns a URL the webview can feed to an <audio> element.
//
// It deliberately does not add path to the allow-list: the backend registers
// files as it produces them (Download, Separate) or as the user picks them
// (PickAudioFile). Allowing here would let the webview turn the media endpoint
// into an arbitrary-file reader, which is exactly what the allow-list exists to
// prevent.
func (a *App) MediaURL(path string) string {
	if path == "" {
		return ""
	}
	return a.media.URL(path)
}

func (a *App) ClearLog() { a.bus.ClearHistory() }

// --- internals --------------------------------------------------------------

// runJob serialises long-running work, wires up cancellation and keeps the
// busy/progress events consistent even when the work fails.
func (a *App) runJob(phase string, fn func(ctx context.Context) error) error {
	a.jobMu.Lock()
	if a.jobCancel != nil {
		running := a.jobPhase
		a.jobMu.Unlock()
		return fmt.Errorf("đang chạy tác vụ khác (%s), vui lòng chờ hoặc bấm Huỷ", running)
	}
	ctx, cancel := context.WithCancel(a.context())
	a.jobCancel, a.jobPhase = cancel, phase
	a.jobMu.Unlock()

	a.bus.SetBusy(true, phase)
	defer func() {
		cancel()
		a.jobMu.Lock()
		a.jobCancel, a.jobPhase = nil, ""
		a.jobMu.Unlock()
		a.bus.SetBusy(false, phase)
	}()

	err := fn(ctx)
	switch {
	case err == nil:
		a.bus.Idle()
		return nil
	case ctx.Err() != nil:
		a.bus.Log(bus.LevelWarn, "Đã huỷ.")
		a.bus.Idle()
		return errors.New("đã huỷ")
	default:
		a.bus.Logf(bus.LevelError, "%v", err)
		a.bus.Progress(bus.Progress{Phase: "error", Percent: -1, Label: "Lỗi", Detail: err.Error()})
		return err
	}
}

func (a *App) lookupModel(ctx context.Context, modelID string) (engine.Backend, engine.Model, error) {
	backendID, _ := engine.SplitID(modelID)
	var backend engine.Backend
	switch backendID {
	case engine.BackendDemucs:
		backend = a.demucs
	case engine.BackendRoformer:
		backend = a.roformer
	default:
		return nil, engine.Model{}, fmt.Errorf("engine không hợp lệ trong %q", modelID)
	}
	for _, m := range backend.Models(ctx) {
		if m.ID == modelID {
			return backend, m, nil
		}
	}
	return nil, engine.Model{}, fmt.Errorf("không tìm thấy model %q", modelID)
}

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// reporter adapts the event bus to the engines' Reporter interface.
func (a *App) reporter() *reporter { return &reporter{bus: a.bus} }

type reporter struct{ bus *bus.Bus }

func (r *reporter) Log(level, text string) { r.bus.Log(level, text) }

func (r *reporter) Logf(level, format string, args ...any) { r.bus.Logf(level, format, args...) }

func (r *reporter) Step(phase string, fraction float64, label, detail string) {
	percent := -1.0
	if fraction >= 0 {
		percent = fraction * 100
		if percent > 100 {
			percent = 100
		}
	}
	r.bus.Progress(bus.Progress{Phase: phase, Percent: percent, Label: label, Detail: detail})
}

var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

// maxNameBytes caps a folder name. Most filesystems allow 255 bytes per
// component; staying well under that leaves room for the stem filenames nested
// inside.
const maxNameBytes = 120

// safeName makes a string usable as a folder name on both Linux and Windows.
//
// Truncation has to respect rune boundaries. yt-dlp titles routinely exceed the
// cap, and a byte-slice cut through a multi-byte rune produces a directory name
// that is not valid UTF-8 — which survives on Linux but comes back through
// encoding/json as U+FFFD, so every path the frontend then holds points at a
// file that does not exist (silent 404 in the player, "không tồn tại" from
// OpenPath). The trailing-punctuation trim also has to run *after* the cut,
// because a cut can expose a new trailing '.' or ' ', which Windows rejects.
func safeName(s string) string {
	s = unsafeChars.ReplaceAllString(s, "_")
	s = strings.TrimSpace(s)

	if len(s) > maxNameBytes {
		cut := s[:maxNameBytes]
		// Drop the partial rune, if any, that the cut left behind.
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		s = cut
	}

	s = strings.Trim(strings.TrimSpace(s), ". ")
	if s == "" {
		s = "track"
	}
	return s
}

func baseNameNoExt(p string) string {
	base := filepath.Base(p)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func formatDuration(seconds float64) string {
	if seconds <= 0 {
		return "?"
	}
	d := time.Duration(seconds * float64(time.Second))
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, u := range []string{"KB", "MB", "GB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f TB", value/unit)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
