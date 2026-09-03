// Package deps locates the external tools the app drives and reports what is
// missing. Nothing here mutates the user's system; see install.go for that.
//
// Lookup order for every tool, first hit wins:
//
//  1. an explicit path the user set in Settings
//  2. the managed Python environment (AppDir/pyenv)
//  3. binaries the app downloaded itself (AppDir/bin)
//  4. binaries shipped next to the executable (ExeDir/bin) — portable installs
//  5. PATH
package deps

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"demucs-studio/internal/paths"
	"demucs-studio/internal/proc"
	"demucs-studio/internal/settings"
)

// Tool ids, mirrored in the frontend.
const (
	ToolYtDlp          = "ytdlp"
	ToolFFmpeg         = "ffmpeg"
	ToolJSRuntime      = "jsRuntime"
	ToolPython         = "python"
	ToolDemucs         = "demucs"
	ToolAudioSeparator = "audioSeparator"
)

// jsRuntimeNames are the runtimes yt-dlp can use to solve YouTube's JavaScript
// challenges, in yt-dlp's own preference order. The basenames double as the
// runtime names in yt-dlp's `--js-runtimes RUNTIME[:PATH]` syntax.
//
// yt-dlp only auto-enables deno; node and bun work but must be named
// explicitly, which is why the app passes the flag rather than relying on
// auto-detection.
var jsRuntimeNames = []string{"deno", "node", "bun"}

// Tool is one external dependency and how to invoke it.
type Tool struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Found   bool   `json:"found"`
	Path    string `json:"path"`
	Version string `json:"version"`
	Source  string `json:"source"` // settings | venv | app | bundled | path
	Hint    string `json:"hint"`

	// Argv is the full command prefix to run the tool. For Python-based tools
	// this may be ["<python>", "-m", "demucs.separate"] when the console script
	// is absent but the module is importable.
	Argv []string `json:"argv"`

	// Required marks tools without which the core flow cannot work.
	Required bool `json:"required"`
	// CanInstall marks tools the app can install unattended.
	CanInstall bool `json:"canInstall"`
}

// GPU describes whether the resolved torch install can actually run kernels on
// this machine's GPU. Available means "verified by executing a CUDA op", not
// merely that torch.cuda.is_available() said yes — see DetectGPU.
type GPU struct {
	Checked   bool   `json:"checked"`
	Available bool   `json:"available"`
	Name      string `json:"name"`
	Torch     string `json:"torch"`
	// Capability is the card's CUDA compute capability, like "5.2", and Driver
	// the installed driver version, like "580.173.02". Together they decide
	// which PyTorch CUDA index can drive this machine, so the UI needs both
	// before any engine is installed — hence the nvidia-smi fallback in
	// DetectGPU, which answers without torch being present.
	Capability string `json:"capability"`
	Driver     string `json:"driver"`
	// Reason explains a false Available when a GPU is nonetheless present.
	Reason string `json:"reason"`
}

// Report is the whole dependency picture handed to the UI.
type Report struct {
	Tools      []Tool `json:"tools"`
	GPU        GPU    `json:"gpu"`
	AppDir     string `json:"appDir"`
	ModelsDir  string `json:"modelsDir"`
	BinDir     string `json:"binDir"`
	ReadyToRip bool   `json:"readyToRip"` // yt-dlp + ffmpeg present
}

// Resolver caches detection results; call Invalidate after installing anything.
type Resolver struct {
	mu    sync.Mutex
	get   func() settings.Settings
	cache map[string]Tool
	gpu   GPU
}

func NewResolver(get func() settings.Settings) *Resolver {
	return &Resolver{get: get, cache: map[string]Tool{}}
}

// Invalidate drops the cached tool lookups. It deliberately keeps the GPU
// probe: that costs several seconds (it imports torch and launches a CUDA
// kernel) and installing yt-dlp or editing a slider cannot change the answer.
//
// Dropping it here used to leave the UI permanently showing "checking GPU…",
// because Report(ctx, false) copies the cached value and nothing re-probes.
func (r *Resolver) Invalidate() {
	r.mu.Lock()
	r.cache = map[string]Tool{}
	r.mu.Unlock()
}

// InvalidateAll additionally forgets the GPU probe. Use it after something that
// can genuinely change which torch is installed — i.e. an engine install.
func (r *Resolver) InvalidateAll() {
	r.mu.Lock()
	r.cache = map[string]Tool{}
	r.gpu = GPU{}
	r.mu.Unlock()
}

func (r *Resolver) cached(id string) (Tool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.cache[id]
	return t, ok
}

func (r *Resolver) store(t Tool) Tool {
	r.mu.Lock()
	r.cache[t.ID] = t
	r.mu.Unlock()
	return t
}

// YtDlp resolves the YouTube downloader.
func (r *Resolver) YtDlp(ctx context.Context) Tool {
	if t, ok := r.cached(ToolYtDlp); ok {
		return t
	}
	t := Tool{
		ID: ToolYtDlp, Label: "yt-dlp", Required: true, CanInstall: true,
		Hint: "Tải video/audio từ YouTube. App có thể tự tải bản standalone.",
	}
	// yt-dlp on Windows releases as yt-dlp.exe; on Linux as yt-dlp_linux.
	if path, source := r.locate(r.get().YtDlpPath, "yt-dlp", "yt-dlp_linux", "yt-dlp_macos"); path != "" {
		t.Found, t.Path, t.Source, t.Argv = true, path, source, []string{path}
		if v, ok := probe(ctx, path, "--version"); ok {
			t.Version = v
		}
	}
	return r.store(t)
}

// FFmpeg resolves ffmpeg, which yt-dlp needs for the WAV conversion.
func (r *Resolver) FFmpeg(ctx context.Context) Tool {
	if t, ok := r.cached(ToolFFmpeg); ok {
		return t
	}
	t := Tool{
		ID: ToolFFmpeg, Label: "ffmpeg", Required: true, CanInstall: true,
		Hint: "Chuyển audio sang WAV/FLAC/MP3. App có thể tự tải bản static.",
	}
	if path, source := r.locate(r.get().FfmpegPath, "ffmpeg"); path != "" {
		t.Found, t.Path, t.Source, t.Argv = true, path, source, []string{path}
		if v, ok := probe(ctx, path, "-version"); ok {
			t.Version = firstToken(v, 3)
		}
	}
	return r.store(t)
}

// JSRuntime resolves a JavaScript engine for yt-dlp.
//
// Modern YouTube requires solving a JS challenge to get playable media URLs.
// Without a runtime yt-dlp warns "No supported JavaScript runtime could be
// found", calls that path deprecated, and loses formats — which surfaces as
// "HTTP Error 403: Forbidden" partway through a download rather than as a
// clear error.
func (r *Resolver) JSRuntime(ctx context.Context) Tool {
	if t, ok := r.cached(ToolJSRuntime); ok {
		return t
	}
	t := Tool{
		ID: ToolJSRuntime, Label: "JS runtime", Required: false, CanInstall: true,
		Hint: "yt-dlp cần deno, node hoặc bun để giải thử thách JavaScript của YouTube. " +
			"Thiếu nó, một số video sẽ lỗi 403. Bấm Cài tự động để app tải deno về.",
	}
	for _, name := range jsRuntimeNames {
		path, source := r.locate("", name)
		if path == "" {
			continue
		}
		v, ok := probe(ctx, path, "--version")
		if !ok {
			continue
		}
		t.Found, t.Path, t.Source, t.Argv = true, path, source, []string{path}
		t.Label = "JS runtime (" + name + ")"
		t.Version = firstToken(v, 2)
		break
	}
	return r.store(t)
}

// JSRuntimeSpec returns the value for yt-dlp's --js-runtimes flag
// ("node:/usr/bin/node"), or "" when no runtime is installed.
func (r *Resolver) JSRuntimeSpec(ctx context.Context) string {
	t := r.JSRuntime(ctx)
	if !t.Found {
		return ""
	}
	name := strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
	return name + ":" + t.Path
}

// Python resolves an interpreter capable of hosting demucs / audio-separator.
func (r *Resolver) Python(ctx context.Context) Tool {
	if t, ok := r.cached(ToolPython); ok {
		return t
	}
	t := Tool{
		ID: ToolPython, Label: "Python 3", Required: false, CanInstall: false,
		Hint: "Cần Python 3.9+ để chạy Demucs / audio-separator.",
	}
	for _, cand := range r.pythonCandidates() {
		argv := resolvePython(cand)
		if argv == nil {
			continue
		}
		v, ok := probeArgv(ctx, argv, "--version")
		// An interpreter that exits zero but does not identify itself as
		// Python 3 is not one we can use.
		if !ok || !looksLikePython3(v) {
			continue
		}
		t.Found, t.Path, t.Version, t.Argv = true, argv[0], v, argv
		t.Source = sourceOf(argv[0])
		break
	}
	return r.store(t)
}

// Demucs resolves how to run demucs: the console script if present, otherwise
// `python -m demucs.separate` against a Python that can import it.
func (r *Resolver) Demucs(ctx context.Context) Tool {
	if t, ok := r.cached(ToolDemucs); ok {
		return t
	}
	t := Tool{
		ID: ToolDemucs, Label: "Demucs", Required: false, CanInstall: true,
		Hint: "Engine tách nhạc Hybrid Transformer Demucs (htdemucs_ft).",
	}
	if path, source := r.locate(r.get().DemucsPath, "demucs"); path != "" {
		if v, ok := probe(ctx, path, "--help"); ok && strings.Contains(v, "usage") {
			t.Found, t.Path, t.Source, t.Argv = true, path, source, []string{path}
			t.Version = pythonModuleVersion(ctx, r.pythonFor(path), "demucs")
			return r.store(t)
		}
	}
	// Fall back to any interpreter that can import demucs.
	for _, py := range r.importCapablePythons(ctx, "demucs") {
		t.Found, t.Path, t.Source = true, py[0], sourceOf(py[0])
		t.Argv = append(append([]string{}, py...), "-m", "demucs.separate")
		t.Version = pythonModuleVersion(ctx, py, "demucs")
		break
	}
	return r.store(t)
}

// AudioSeparator resolves the BS-RoFormer / Mel-Band RoFormer runner.
func (r *Resolver) AudioSeparator(ctx context.Context) Tool {
	if t, ok := r.cached(ToolAudioSeparator); ok {
		return t
	}
	t := Tool{
		ID: ToolAudioSeparator, Label: "audio-separator", Required: false, CanInstall: true,
		Hint: "Engine chạy các model BS-RoFormer / Mel-Band RoFormer.",
	}
	if path, source := r.locate(r.get().AudioSeparatorPath, "audio-separator"); path != "" {
		if v, ok := probe(ctx, path, "--version"); ok {
			t.Found, t.Path, t.Source, t.Argv = true, path, source, []string{path}
			t.Version = strings.TrimSpace(strings.TrimPrefix(v, "audio-separator"))
			return r.store(t)
		}
	}
	for _, py := range r.importCapablePythons(ctx, "audio_separator") {
		t.Found, t.Path, t.Source = true, py[0], sourceOf(py[0])
		t.Argv = append(append([]string{}, py...), "-m", "audio_separator.utils.cli")
		t.Version = pythonModuleVersion(ctx, py, "audio_separator")
		break
	}
	return r.store(t)
}

// Report gathers every tool. checkGPU is slow (imports torch) so it is opt-in.
func (r *Resolver) Report(ctx context.Context, checkGPU bool) Report {
	tools := []Tool{
		r.YtDlp(ctx),
		r.FFmpeg(ctx),
		r.JSRuntime(ctx),
		r.Python(ctx),
		r.Demucs(ctx),
		r.AudioSeparator(ctx),
	}
	rep := Report{
		Tools:     tools,
		AppDir:    paths.AppDir(),
		ModelsDir: paths.ModelsDir(),
		BinDir:    paths.BinDir(),
	}
	// Derive readiness from the Required flag rather than slice positions, so
	// inserting a tool cannot silently change what "ready" means.
	rep.ReadyToRip = true
	for _, t := range tools {
		if t.Required && !t.Found {
			rep.ReadyToRip = false
		}
	}
	if checkGPU {
		rep.GPU = r.DetectGPU(ctx)
	} else {
		r.mu.Lock()
		rep.GPU = r.gpu
		r.mu.Unlock()
	}
	return rep
}

// gpuProbe reports what torch can really do with the GPU.
//
// torch.cuda.is_available() is not enough: a PyTorch wheel only ships kernels
// for the compute capabilities it was built against, so an older card (e.g. a
// GTX 900-series, sm_52, against a cu12x/cu13x build) reports available=True and
// then dies mid-separation with "no kernel image is available for execution on
// the device". The only trustworthy answer is to launch a real kernel, which is
// what the smoke test at the end does.
const gpuProbe = `import json, torch
out = {"torch": torch.__version__, "available": False, "name": "", "reason": ""}
try:
    if not torch.cuda.is_available():
        out["reason"] = "torch không thấy CUDA"
    else:
        out["name"] = torch.cuda.get_device_name(0)
        major, minor = torch.cuda.get_device_capability(0)
        out["capability"] = "%d.%d" % (major, minor)
        sm = "sm_%d%d" % (major, minor)
        arches = [a for a in torch.cuda.get_arch_list() if a.startswith("sm_")]
        # A cubin built for sm_XY runs on any card of the same major version
        # with an equal or higher minor, which is what torch's own warning
        # means by "5.0 which supports hardware CC >=5.0,<6.0". Comparing the
        # string exactly instead rejected a GTX 960 (sm_52) against a build
        # carrying sm_50 — a GPU torch could drive perfectly well.
        def runs_here(arch):
            digits = arch[3:]
            if not digits.isdigit() or len(digits) < 2:
                return False
            return int(digits[:-1]) == major and int(digits[-1]) <= minor
        if arches and not any(runs_here(a) for a in arches):
            out["reason"] = (
                "GPU %s (%s) không nằm trong các kiến trúc torch hỗ trợ (%s)"
                % (out["name"], sm, ", ".join(arches))
            )
        else:
            # Launch an actual kernel; anything less can still fail later.
            torch.zeros(64, device="cuda").add_(1).sum().item()
            torch.cuda.synchronize()
            # And then a convolution, because that is what the models actually
            # run and it goes through cuDNN, which fails independently of CUDA:
            # a cuDNN from the wrong CUDA major, or one that dropped this
            # architecture, breaks convolutions while elementwise kernels keep
            # working. Testing only the latter is what let this report a ready
            # GPU and then die mid-separation with "GET was unable to find an
            # engine to execute this computation".
            torch.nn.functional.conv1d(
                torch.zeros(1, 1, 64, device="cuda"),
                torch.zeros(1, 1, 3, device="cuda"),
            )
            torch.cuda.synchronize()
            out["available"] = True
except Exception as exc:
    out["reason"] = "%s: %s" % (type(exc).__name__, exc)
print("GPUPROBE" + json.dumps(out))`

// CachedGPU returns what DetectGPU last found, without probing.
//
// For callers that want to describe the machine but must not block: the probe
// imports torch and can take seconds, and startup already warms it in the
// background. A zero GPU (Checked false) simply means "not known yet", and the
// UI re-asks when the app:deps event arrives.
func (r *Resolver) CachedGPU() GPU {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gpu
}

// TorchOutsideVenv reports whether a usable torch exists outside the managed
// environment, which is the only thing "reuse" can actually reuse.
func (r *Resolver) TorchOutsideVenv(ctx context.Context) bool {
	py := r.torchPython(ctx)
	return len(py) > 0 && !strings.HasPrefix(py[0], paths.VenvDir())
}

// DetectGPU asks torch whether CUDA is usable. Cached, because importing torch
// costs several seconds.
func (r *Resolver) DetectGPU(ctx context.Context) GPU {
	r.mu.Lock()
	if r.gpu.Checked {
		g := r.gpu
		r.mu.Unlock()
		return g
	}
	r.mu.Unlock()

	g := GPU{Checked: true}
	py := r.torchPython(ctx)
	if len(py) == 0 {
		g.Reason = "chưa tìm thấy PyTorch"
		r.mu.Lock()
		r.gpu = describeCard(ctx, g)
		g = r.gpu
		r.mu.Unlock()
		return g
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := proc.Output(ctx, proc.Options{
		Bin:      py[0],
		Args:     append(append([]string{}, py[1:]...), "-c", gpuProbe),
		ExtraEnv: []string{"PYTHONUNBUFFERED=1"},
	})
	if err != nil {
		g.Reason = "không chạy được kiểm tra CUDA: " + err.Error()
	} else if payload := findProbePayload(out); payload != "" {
		var parsed struct {
			Torch      string `json:"torch"`
			Available  bool   `json:"available"`
			Name       string `json:"name"`
			Capability string `json:"capability"`
			Reason     string `json:"reason"`
		}
		if jsonErr := json.Unmarshal([]byte(payload), &parsed); jsonErr == nil {
			g.Torch, g.Available, g.Name, g.Reason =
				parsed.Torch, parsed.Available, parsed.Name, parsed.Reason
			g.Capability = parsed.Capability
		} else {
			g.Reason = "không đọc được kết quả kiểm tra CUDA"
		}
	} else {
		g.Reason = "kiểm tra CUDA không trả về kết quả"
	}
	g.Reason = withArchHint(g.Reason)
	g = describeCard(ctx, g)

	r.mu.Lock()
	r.gpu = g
	r.mu.Unlock()
	return g
}

// describeCard fills in whatever the torch probe could not tell us about the
// hardware, by asking the driver instead.
//
// Doing this on every path, not just when torch is missing, is the point. The
// probe reports a capability only when torch.cuda.is_available() succeeded, so
// the single most important case — an NVIDIA card with a CPU-only torch, which
// is what sends people to the install panel in the first place — used to yield
// an empty capability and a panel with no verdict at all. Card identity is a
// fact about the machine and does not depend on torch working.
func describeCard(ctx context.Context, g GPU) GPU {
	if g.Capability != "" && g.Name != "" && g.Driver != "" {
		return g
	}
	name, capability, driver := smiCard(ctx)
	if g.Name == "" {
		g.Name = name
	}
	if g.Capability == "" {
		g.Capability = capability
	}
	if g.Driver == "" {
		g.Driver = driver
	}
	return g
}

// smiCard asks the driver for the card's name, compute capability and driver
// version. These are facts about the machine, independent of whether torch is
// installed or works, which is why DetectGPU consults it whenever the probe
// could not supply them rather than only when torch is missing entirely.
//
// probe returns just the first non-empty line, so on a multi-GPU box this
// describes device 0 — the same device the torch probe measures.
//
// Empty strings mean no answer: no nvidia-smi (the normal case without an
// NVIDIA card), or a driver too old to know a field, which prints
// "[Not Supported]" and is rejected here rather than passed on as if it were a
// version.
func smiCard(ctx context.Context) (name, capability, driver string) {
	out, ok := probe(ctx, "nvidia-smi",
		"--query-gpu=name,compute_cap,driver_version", "--format=csv,noheader")
	if !ok {
		return "", "", ""
	}
	fields := strings.Split(out, ",")
	if len(fields) != 3 {
		return "", "", ""
	}
	name = strings.TrimSpace(fields[0])
	if cap := strings.TrimSpace(fields[1]); looksNumericVersion(cap) {
		capability = cap
	}
	if drv := strings.TrimSpace(fields[2]); looksNumericVersion(drv) {
		driver = drv
	}
	return name, capability, driver
}

// looksNumericVersion keeps non-answers such as "[Not Supported]" or "N/A" out
// of fields the UI turns into a verdict. Without it an unparseable capability
// is still truthy in the webview, and the panel states with confidence that a
// perfectly good card is unsupported.
func looksNumericVersion(s string) bool {
	if s == "" {
		return false
	}
	digits := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits = true
		case r == '.':
		default:
			return false
		}
	}
	return digits
}

// archMismatchMarker is the part of the probe's verdict that says the card's
// compute capability is absent from the arch list torch was built for. It has
// to stay in step with the wording in gpuProbe.
const archMismatchMarker = "không nằm trong các kiến trúc torch hỗ trợ"

// withArchHint appends the way out of an architecture mismatch.
//
// The bare message names the problem and leaves the user stuck: nothing about
// "sm_52 is not in sm_75, sm_80, …" hints that another CUDA index would fix it.
//
// It deliberately does not name one. Which index fits depends on the card, and
// naming a single one got it wrong once already: the advice used to be cu118,
// on the assumption that every current build had dropped Maxwell — but cu126
// still carries sm_50 (verified with cuobjdump) and gives torch 2.14 instead of
// 2.7.1. The install panel now lists the compatible cards per index and marks
// the one that fits this machine, so point there instead of guessing.
func withArchHint(reason string) string {
	if !strings.Contains(reason, archMismatchMarker) {
		return reason
	}
	return reason + ". Mở tab Phụ thuộc → PyTorch “Tải bản CUDA (GPU)”: " +
		"ô CUDA sẽ hiện dòng card mà mỗi bản hỗ trợ và đánh dấu bản phù hợp với máy bạn"
}

// findProbePayload pulls the JSON line out of output that torch may have
// polluted with warnings.
func findProbePayload(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "GPUPROBE"); ok {
			return rest
		}
	}
	return ""
}

// FFmpegDir is the directory to hand yt-dlp via --ffmpeg-location.
func (r *Resolver) FFmpegDir(ctx context.Context) string {
	if t := r.FFmpeg(ctx); t.Found {
		return filepath.Dir(t.Path)
	}
	return ""
}

// locate walks the lookup order for a set of candidate basenames.
func (r *Resolver) locate(override string, names ...string) (path, source string) {
	if override != "" {
		if isExecutable(override) {
			return override, "settings"
		}
		if p, err := exec.LookPath(override); err == nil {
			return p, "settings"
		}
	}
	dirs := []struct{ dir, source string }{
		{filepath.Dir(paths.VenvBin("python")), "venv"},
		{paths.BinDir(), "app"},
		{paths.BundledBinDir(), "bundled"},
	}
	for _, name := range names {
		for _, d := range dirs {
			cand := filepath.Join(d.dir, paths.Exe(name))
			if isExecutable(cand) {
				return cand, d.source
			}
		}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p, "path"
		}
	}
	return "", ""
}

// pythonCandidates lists interpreter command lines to try, managed venv first.
//
// Entries are full argv slices rather than plain paths because the recommended
// way to reach Python on Windows is the `py` launcher, which needs an argument
// (`py -3`) to pick a version.
func (r *Resolver) pythonCandidates() [][]string {
	out := [][]string{}
	if custom := r.get().PythonPath; custom != "" {
		out = append(out, []string{custom})
	}
	out = append(out, []string{paths.VenvBin("python")})

	if runtime.GOOS == "windows" {
		out = append(out,
			// The py launcher first: it resolves a real installation even when
			// PATH only holds the Microsoft Store alias.
			[]string{"py", "-3"},
			[]string{"python"},
			[]string{"python3"},
		)
	} else {
		for _, name := range []string{
			"python3.12", "python3.11", "python3.10", "python3.13", "python3.9",
			"python3", "python",
		} {
			out = append(out, []string{name})
		}
	}
	return out
}

// resolvePython turns a candidate argv into an absolute one, or nil when the
// interpreter does not exist.
func resolvePython(argv []string) []string {
	if len(argv) == 0 || argv[0] == "" {
		return nil
	}
	head := argv[0]
	if filepath.IsAbs(head) {
		if !isExecutable(head) {
			return nil
		}
	} else {
		p, err := exec.LookPath(head)
		if err != nil {
			return nil
		}
		head = p
	}
	if isStoreStub(head) {
		return nil
	}
	return append([]string{head}, argv[1:]...)
}

// isStoreStub rejects Windows' "App Execution Alias" for Python. When Python is
// not installed, %LOCALAPPDATA%\Microsoft\WindowsApps\python.exe still exists
// and is on PATH, but running it opens the Microsoft Store instead of an
// interpreter — so accepting it would leave the app permanently "installing".
func isStoreStub(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	return strings.Contains(strings.ToLower(path), `\windowsapps\`)
}

// looksLikePython3 guards against anything that exits zero without being a
// Python 3 interpreter.
func looksLikePython3(version string) bool {
	return strings.HasPrefix(strings.TrimSpace(version), "Python 3")
}

// importCapablePythons returns interpreter argvs that can import the given
// module, managed venv first.
func (r *Resolver) importCapablePythons(ctx context.Context, module string) [][]string {
	var out [][]string
	seen := map[string]bool{}
	for _, cand := range r.pythonCandidates() {
		argv := resolvePython(cand)
		if argv == nil {
			continue
		}
		key := strings.Join(argv, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		if canImport(ctx, argv, module) {
			out = append(out, argv)
		}
	}
	return out
}

// torchPython finds an interpreter with torch, preferring the one already
// backing a resolved engine so the reported GPU matches what will actually run.
func (r *Resolver) torchPython(ctx context.Context) []string {
	for _, t := range []Tool{r.Demucs(ctx), r.AudioSeparator(ctx)} {
		if !t.Found {
			continue
		}
		// The "python -m module" form carries its interpreter in Argv; strip
		// the trailing "-m <module>" to get the bare interpreter command.
		if i := indexOf(t.Argv, "-m"); i > 0 {
			return t.Argv[:i]
		}
		if py := r.pythonFor(t.Path); py != nil && canImport(ctx, py, "torch") {
			return py
		}
	}
	if list := r.importCapablePythons(ctx, "torch"); len(list) > 0 {
		return list[0]
	}
	return nil
}

// pythonFor guesses the interpreter that owns a console script, e.g.
// /x/venv/bin/demucs -> /x/venv/bin/python.
func (r *Resolver) pythonFor(script string) []string {
	dir := filepath.Dir(script)
	for _, name := range []string{"python3", "python"} {
		cand := filepath.Join(dir, paths.Exe(name))
		if isExecutable(cand) {
			return []string{cand}
		}
	}
	if t, ok := r.cached(ToolPython); ok && t.Found {
		return t.Argv
	}
	return nil
}

func indexOf(list []string, want string) int {
	for i, v := range list {
		if v == want {
			return i
		}
	}
	return -1
}

func canImport(ctx context.Context, python []string, module string) bool {
	if len(python) == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	args := append(append([]string{}, python[1:]...),
		"-c", "import importlib.util,sys; sys.exit(0 if importlib.util.find_spec('"+module+"') else 1)")
	err := proc.Run(ctx, proc.Options{
		Bin:  python[0],
		Args: args,
	})
	return err == nil
}

func pythonModuleVersion(ctx context.Context, python []string, module string) string {
	if len(python) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := proc.Output(ctx, proc.Options{
		Bin: python[0],
		Args: append(append([]string{}, python[1:]...), "-c",
			"import importlib.metadata as m; print(m.version('"+strings.ReplaceAll(module, "_", "-")+"'))"),
	})
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func probe(ctx context.Context, bin string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return proc.Probe(ctx, bin, args...)
}

// probeArgv runs a command whose prefix already carries arguments, such as the
// Windows `py -3` launcher.
func probeArgv(ctx context.Context, argv []string, extra ...string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	return probe(ctx, argv[0], append(append([]string{}, argv[1:]...), extra...)...)
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return st.Mode()&0o111 != 0
}

// sourceOf labels where a resolved path came from, for display.
func sourceOf(p string) string {
	switch {
	case strings.HasPrefix(p, paths.VenvDir()):
		return "venv"
	case strings.HasPrefix(p, paths.BinDir()):
		return "app"
	case strings.HasPrefix(p, paths.BundledBinDir()):
		return "bundled"
	default:
		return "path"
	}
}

func firstToken(s string, n int) string {
	parts := strings.Fields(s)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, " ")
}
