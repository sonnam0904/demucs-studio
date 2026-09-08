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
	"demucs-studio/internal/selfupdate"
	"demucs-studio/internal/settings"
	"demucs-studio/internal/suno"
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

	// Startup is the first moment the previous version is no longer the running
	// image, so it is the only safe time to delete what an update left behind.
	go selfupdate.CleanupOld()

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
	// Accels and Devices are the values this machine may be offered, decided by
	// settings.AccelApplies — the same rule the store normalises a copied
	// settings file with and the installer refuses by. Shipped as data because
	// the frontend used to re-derive it from the platform string, which made
	// the UI a third implementation of the rule, and the one that decides what
	// the user can click: when it drifted, the panel offered MPS on Linux and
	// picking it replaced a working CUDA torch with the CPU wheel.
	Accels  []string `json:"accels"`
	Devices []string `json:"devices"`
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
		Accels:     settings.AccelsFor(runtime.GOOS, runtime.GOARCH),
		Devices:    settings.DevicesFor(runtime.GOOS, runtime.GOARCH),
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
		case deps.ToolJSRuntime:
			err = deps.InstallDeno(ctx, rep)
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

// CudaTargets lists the selectable PyTorch CUDA indexes, each with the card
// generations it can drive and whether it fits the GPU in this machine.
//
// The card list is not decoration: a wheel only carries machine code for the
// compute capabilities it was built for, and the sets differ enough between
// indexes that the wrong pick yields a torch which imports, reports CUDA as
// available, and then has no kernel to run.
// It reads the cached probe result rather than triggering one: the frontend
// calls this while laying out the install panel, and DetectGPU imports torch,
// which would put seconds on the startup path — and race the warm-up goroutine
// into a second concurrent torch import. Before the probe has answered every
// entry simply carries no verdict, and the UI re-asks when app:deps arrives.
func (a *App) CudaTargets() []deps.CudaTarget {
	g := a.resolver.CachedGPU()
	return deps.CudaTargetsFor(g.Capability, g.Driver)
}

// deviceNote is a line resolveDevice wants shown, kept as data so the decision
// stays a pure function the tests can drive.
type deviceNote struct{ level, text string }

// resolveDevice turns the stored device preference into the exact string an
// engine backend will receive, plus whatever the user needs told about it.
//
// Always explicit, never "auto": both engines fall back to their own
// torch.cuda.is_available() check, which is the very thing that crashes on a
// GPU whose torch has no kernels for it.
//
// Whether a backend can actually honour the answer is the backend's own
// business — audio-separator uses MPS no matter what it is handed, and it says
// so itself. Deciding that here would mean gating on gpu.Backend, which is only
// filled in once the probe passes, so the caveat would go missing on precisely
// the Mac where MPS is broken and the user picked CPU to escape it.
func resolveDevice(preference string, gpu deps.GPU) (string, []deviceNote) {
	var notes []deviceNote
	warn := func(format string, args ...any) {
		notes = append(notes, deviceNote{bus.LevelWarn, fmt.Sprintf(format, args...)})
	}

	device := preference
	switch preference {
	case "auto":
		// gpu.Backend, not a hardcoded "cuda": the verified accelerator is
		// "mps" on Apple Silicon, and resolving auto to cuda there would send a
		// Mac down a path its torch has no CUDA for. The Backend != "" guard
		// keeps a payload that claims availability without naming a backend
		// from producing an empty device, which every downstream consumer
		// silently drops — handing the choice back to the engine.
		if gpu.Available && gpu.Backend != "" {
			device = gpu.Backend
		} else {
			device = "cpu"
			if gpu.Reason != "" {
				notes = append(notes, deviceNote{bus.LevelInfo, "Dùng CPU: " + gpu.Reason})
			}
		}
	case "cuda", "mps":
		// A saved choice can outlive the machine it was made on — settings
		// travel with the data directory — so the requested backend has to
		// match what actually works here, not merely be some working GPU.
		if !gpu.Available || gpu.Backend != device {
			warn("Đã chọn %s nhưng không dùng được (%s) — chuyển sang CPU.", device, orDash(gpu.Reason))
			device = "cpu"
		}
	case "cpu":
	default:
		warn("Thiết bị %q không hợp lệ — dùng CPU.", preference)
		device = "cpu"
	}

	return device, notes
}

// --- self-update ------------------------------------------------------------

// CheckUpdate asks GitHub whether a newer release exists. The UI calls it once
// at startup and again from the "kiểm tra lại" button.
func (a *App) CheckUpdate() (selfupdate.Status, error) {
	return selfupdate.Check(a.context(), appVersion)
}

// ApplyUpdate downloads the new release, swaps it over this install and
// restarts into it.
//
// The restart is deliberately the last thing that happens and is not undoable:
// once Relaunch succeeds a second copy is starting up, so this process has to
// go. If the swap itself fails, Apply has already put the old install back and
// the error surfaces in the UI with nothing changed.
func (a *App) ApplyUpdate() error {
	st, err := selfupdate.Check(a.context(), appVersion)
	if err != nil {
		return err
	}
	if !st.Available {
		return errors.New("đang dùng bản mới nhất")
	}
	if !st.CanApply {
		return errors.New(st.Reason)
	}

	if err := a.runJob("update", func(ctx context.Context) error {
		return selfupdate.Apply(ctx, st, a.reporter())
	}); err != nil {
		return err
	}

	if err := selfupdate.Relaunch(); err != nil {
		// The new version is installed and will be picked up next time the user
		// opens the app themselves, so this is a warning rather than a failure.
		a.bus.Logf(bus.LevelWarn, "Đã cài bản %s nhưng không tự mở lại được (%v). Hãy đóng và mở lại ứng dụng.", st.Latest, err)
		return fmt.Errorf("đã cài bản %s, nhưng cần mở lại ứng dụng thủ công", st.Latest)
	}
	// Give the replacement a moment to get going before this window disappears,
	// so the user never sees an empty desktop.
	go func() {
		time.Sleep(500 * time.Millisecond)
		wruntime.Quit(a.context())
	}()
	return nil
}

// SuggestedAccel tells the UI which torch flavour to preselect: reuse an
// existing torch when there is one that actually works, else GPU if an NVIDIA
// driver looks present, else CPU.
//
// Two conditions, and both matter. The torch has to actually work — a CPU-only
// one reports a version just like a CUDA one, so keying off mere presence made
// this a trap where a machine with an NVIDIA card stayed pinned to CPU through
// any number of reinstalls. And it has to live *outside* the managed venv,
// because that is all "reuse" can reuse: suggesting it for the app's own venv
// torch pointed the user at an install path that installs no torch at all.
func (a *App) SuggestedAccel() string {
	ctx := a.context()
	if a.resolver.DetectGPU(ctx).Available && a.resolver.TorchOutsideVenv(ctx) {
		return "reuse"
	}
	// Apple Silicon has a usable GPU on every unit, and the macOS wheel always
	// carries Metal support, so there is nothing to detect. Asked of
	// AccelApplies rather than spelling out "darwin && arm64" again: this
	// function suggests what the store will later normalise and the installer
	// will later accept, so a fourth copy of the rule here could only ever
	// disagree with those two — and would do it by preselecting a flavour the
	// install then refuses.
	if settings.AccelApplies(runtime.GOOS, runtime.GOARCH, "mps") {
		return "mps"
	}
	if settings.AccelApplies(runtime.GOOS, runtime.GOARCH, "cuda") {
		if _, err := exec.LookPath("nvidia-smi"); err == nil {
			return "cuda"
		}
	}
	return "cpu"
}

// --- YouTube / Suno ---------------------------------------------------------

func (a *App) FetchInfo(url string) (ytdl.Info, error) {
	ctx := a.context()
	a.bus.Progress(bus.Progress{Phase: "info", Percent: -1, Label: "Đang đọc thông tin video"})
	defer a.bus.Idle()

	var (
		info ytdl.Info
		err  error
	)
	if suno.IsURL(url) {
		var song suno.Song
		if song, err = suno.Resolve(ctx, url); err == nil {
			info = sunoInfo(song)
		}
	} else {
		tool := a.resolver.YtDlp(ctx)
		if !tool.Found {
			return ytdl.Info{}, errors.New("chưa có yt-dlp. Mở tab Phụ thuộc để cài")
		}
		set := a.settings.Get()
		info, err = ytdl.FetchInfo(ctx, ytdl.Options{
			YtDlp:              tool.Path,
			URL:                url,
			CookiesFromBrowser: set.CookiesFromBrowser,
			JSRuntime:          a.resolver.JSRuntimeSpec(ctx),
		}, a.reporter())
	}
	if err != nil {
		a.bus.Logf(bus.LevelError, "%v", err)
		return ytdl.Info{}, err
	}
	a.bus.Logf(bus.LevelInfo, "%s — %s (%s)", info.Title, info.Uploader, formatDuration(info.Duration))
	return info, nil
}

// sunoInfo maps a resolved Suno song onto the shape the UI already renders.
func sunoInfo(song suno.Song) ytdl.Info {
	return ytdl.Info{
		ID:         song.ID,
		Title:      song.Title,
		Uploader:   song.Uploader,
		Duration:   song.Duration,
		Thumbnail:  song.Thumbnail,
		WebpageURL: song.WebpageURL,
	}
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
		jsRuntime := a.resolver.JSRuntimeSpec(ctx)

		// What yt-dlp is actually pointed at. For YouTube that is the pasted
		// link, and ytdl.Download extracts the metadata itself as it goes. Suno
		// needs a step first: yt-dlp refuses suno.com by name, so the page is
		// resolved here to a direct CDN URL — which then carries no metadata of
		// its own, so the resolved song supplies it.
		source := ytdl.SourceYouTube
		fetchURL := url
		filenameBase := ""
		var sunoMeta ytdl.Info

		if suno.IsURL(url) {
			source = "Suno"
			a.bus.Progress(bus.Progress{Phase: "download", Percent: -1, Label: "Đang đọc trang Suno"})
			song, err := suno.Resolve(ctx, url)
			if err != nil {
				return err
			}
			sunoMeta = sunoInfo(song)
			fetchURL = song.MediaURL
			filenameBase = song.Title
			a.bus.Logf(bus.LevelInfo, "Suno: %s — %s", sunoMeta.Title, sunoMeta.Uploader)
		}

		outDir := filepath.Join(set.OutputDir, "downloads")
		got, err := ytdl.Download(ctx, ytdl.Options{
			YtDlp:              tool.Path,
			FfmpegDir:          ffmpegDir,
			URL:                fetchURL,
			OutDir:             outDir,
			Format:             set.AudioFormat,
			Mp3Bitrate:         set.Mp3Bitrate,
			CookiesFromBrowser: set.CookiesFromBrowser,
			JSRuntime:          jsRuntime,
			FilenameBase:       filenameBase,
			Source:             source,
		}, a.reporter())
		if err != nil {
			return err
		}
		if sunoMeta.Title != "" {
			got.Info = sunoMeta
			got.Duration = sunoMeta.Duration
			got.Title = sunoMeta.Title
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
		device, notes := resolveDevice(set.Device, a.resolver.DetectGPU(ctx))
		for _, n := range notes {
			a.bus.Log(n.level, n.text)
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
	cmd := shellOpen(target)
	a.bus.Logf(bus.LevelDebug, "Mở %s: %s", target, strings.Join(cmd.Args, " "))
	if err := cmd.Start(); err != nil {
		return err
	}
	// Start succeeding only means the helper launched. Whether it then found
	// the path is reported by its exit status, and discarding that is what made
	// the previous Windows bug invisible: the click did nothing and said
	// nothing. Waiting here would block the UI thread, so the status is logged
	// from a goroutine instead.
	go func() {
		err := cmd.Wait()
		if err != nil && shellOpenReportsExit {
			a.bus.Logf(bus.LevelWarn, "Không mở được %s: %v", target, err)
		}
	}()
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
