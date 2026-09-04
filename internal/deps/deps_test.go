package deps

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"demucs-studio/internal/paths"
	"demucs-studio/internal/settings"
)

func testResolver(set settings.Settings) *Resolver {
	return NewResolver(func() settings.Settings { return set })
}

func TestPythonCandidateOrder(t *testing.T) {
	set := settings.Defaults()
	set.PythonPath = "/custom/python3"
	got := testResolver(set).pythonCandidates()

	if len(got) < 3 {
		t.Fatalf("expected several candidates, got %v", got)
	}
	// An explicit override must win over everything else.
	if got[0][0] != "/custom/python3" {
		t.Errorf("first candidate = %v, want the settings override", got[0])
	}
	// The managed venv comes next, so an app-installed engine is preferred over
	// whatever the system happens to have.
	if got[1][0] != paths.VenvBin("python") {
		t.Errorf("second candidate = %v, want the managed venv", got[1])
	}
	for _, argv := range got {
		if len(argv) == 0 || argv[0] == "" {
			t.Errorf("empty candidate in %v", got)
		}
	}
}

func TestPythonCandidatesOmitEmptyOverride(t *testing.T) {
	got := testResolver(settings.Defaults()).pythonCandidates()
	for _, argv := range got {
		if argv[0] == "" {
			t.Fatalf("blank candidate leaked in: %v", got)
		}
	}
	if got[0][0] != paths.VenvBin("python") {
		t.Errorf("without an override the venv should come first, got %v", got[0])
	}
}

func TestWindowsPythonCandidatesUseLauncher(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("candidate list is platform-specific")
	}
	got := testResolver(settings.Defaults()).pythonCandidates()
	var launcher []string
	for _, argv := range got {
		if argv[0] == "py" {
			launcher = argv
		}
	}
	// `py -3` resolves a real installation even when PATH only holds the
	// Microsoft Store alias, so it must be tried before bare "python".
	if launcher == nil {
		t.Fatalf("py launcher missing from %v", got)
	}
	if len(launcher) != 2 || launcher[1] != "-3" {
		t.Errorf("launcher argv = %v, want [py -3]", launcher)
	}
}

func TestLooksLikePython3(t *testing.T) {
	tests := map[string]bool{
		"Python 3.12.1":  true,
		"Python 3.9.18":  true,
		" Python 3.11.9": true,
		// The Store stub and Python 2 must both be rejected.
		"Python 2.7.18":                     false,
		"":                                  false,
		"'python' is not recognized":        false,
		"Microsoft Store: install Python 3": false,
	}
	for in, want := range tests {
		if got := looksLikePython3(in); got != want {
			t.Errorf("looksLikePython3(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsStoreStub(t *testing.T) {
	stub := `C:\Users\x\AppData\Local\Microsoft\WindowsApps\python.exe`
	real := `C:\Python312\python.exe`
	if runtime.GOOS != "windows" {
		// The check is a no-op elsewhere so a POSIX path is never misjudged.
		if isStoreStub(stub) || isStoreStub("/usr/bin/python3") {
			t.Error("isStoreStub must be inert off Windows")
		}
		return
	}
	if !isStoreStub(stub) {
		t.Errorf("expected %q to be detected as the Store alias", stub)
	}
	if isStoreStub(real) {
		t.Errorf("%q is a real interpreter", real)
	}
}

func TestResolvePythonRejectsMissing(t *testing.T) {
	if got := resolvePython(nil); got != nil {
		t.Errorf("nil argv -> %v", got)
	}
	if got := resolvePython([]string{""}); got != nil {
		t.Errorf("empty head -> %v", got)
	}
	if got := resolvePython([]string{"definitely-not-on-path-xyz"}); got != nil {
		t.Errorf("missing binary -> %v", got)
	}
	if got := resolvePython([]string{"/nonexistent/abs/python"}); got != nil {
		t.Errorf("missing absolute path -> %v", got)
	}
}

func TestResolvePythonPreservesArgs(t *testing.T) {
	// Use a binary that exists on every platform this test runs on.
	name := "sh"
	if runtime.GOOS == "windows" {
		name = "cmd"
	}
	got := resolvePython([]string{name, "-3"})
	if got == nil {
		t.Skipf("%s not on PATH", name)
	}
	if len(got) != 2 || got[1] != "-3" {
		t.Errorf("args lost: %v", got)
	}
	if !strings.Contains(got[0], name) {
		t.Errorf("head not resolved to an absolute path: %v", got)
	}
}

func TestIndexOf(t *testing.T) {
	argv := []string{"/usr/bin/python3", "-m", "demucs.separate"}
	if got := indexOf(argv, "-m"); got != 1 {
		t.Errorf("indexOf(-m) = %d, want 1", got)
	}
	if got := indexOf(argv, "--repo"); got != -1 {
		t.Errorf("indexOf(--repo) = %d, want -1", got)
	}
}

// torchPython strips the trailing "-m <module>" to recover the interpreter that
// backs a resolved engine; getting this wrong would probe the wrong Python.
func TestEngineArgvCarriesInterpreter(t *testing.T) {
	argv := append(append([]string{}, "py", "-3"), "-m", "audio_separator.utils.cli")
	i := indexOf(argv, "-m")
	if i != 2 {
		t.Fatalf("indexOf(-m) = %d, want 2", i)
	}
	interp := argv[:i]
	if len(interp) != 2 || interp[0] != "py" || interp[1] != "-3" {
		t.Errorf("interpreter prefix = %v, want [py -3]", interp)
	}
}

func TestYtDlpAssetForEveryTarget(t *testing.T) {
	// The running platform must always have a downloadable yt-dlp, otherwise
	// the one-click installer cannot work at all.
	asset, dest, err := ytDlpAsset()
	if err != nil {
		t.Fatalf("no yt-dlp asset for %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	if asset == "" || dest == "" {
		t.Errorf("asset=%q dest=%q", asset, dest)
	}
	if runtime.GOOS == "windows" && !strings.HasSuffix(dest, ".exe") {
		t.Errorf("Windows destination %q should end in .exe", dest)
	}
	if runtime.GOOS != "windows" && strings.HasSuffix(dest, ".exe") {
		t.Errorf("non-Windows destination %q should not end in .exe", dest)
	}
}

func TestFFmpegAssetsMatchPlatform(t *testing.T) {
	// Checked for every target rather than just the host, so a Linux CI run
	// still catches a broken macOS or Windows mapping.
	for _, tc := range []struct {
		goos, goarch string
		// want is the exact asset name list the installer should download.
		want []string
	}{
		{"windows", "amd64", []string{"ffmpeg-master-latest-win64-gpl.zip"}},
		{"windows", "arm64", []string{"ffmpeg-master-latest-winarm64-gpl.zip"}},
		{"linux", "amd64", []string{"ffmpeg-master-latest-linux64-gpl.tar.xz"}},
		{"linux", "arm64", []string{"ffmpeg-master-latest-linuxarm64-gpl.tar.xz"}},
		// Go's amd64 is Node's x64 in this project's asset names.
		{"darwin", "amd64", []string{"ffmpeg-darwin-x64", "ffprobe-darwin-x64"}},
		{"darwin", "arm64", []string{"ffmpeg-darwin-arm64", "ffprobe-darwin-arm64"}},
	} {
		target := tc.goos + "/" + tc.goarch
		downloads, err := ffmpegAssetsFor(tc.goos, tc.goarch)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		got := make([]string, len(downloads))
		for i, dl := range downloads {
			got[i] = dl.name
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: assets = %v, want %v", target, got, tc.want)
		}

		for _, dl := range downloads {
			if !strings.HasSuffix(dl.url, dl.name) {
				t.Errorf("%s: url %q should end in asset name %q", target, dl.url, dl.name)
			}
			if tc.goos == "darwin" {
				// Bare executables: nothing to extract, and no manifest is
				// published next to them.
				if dl.exe == "" {
					t.Errorf("%s: %q should install as a bare executable", target, dl.name)
				}
				if dl.checksums != "" {
					t.Errorf("%s: %q has no published checksums", target, dl.name)
				}
				continue
			}
			// Elsewhere the extractor is chosen by suffix: zip via
			// archive/zip, tar.xz via `tar`. A bare exe would reach neither.
			if dl.exe != "" {
				t.Errorf("%s: %q should be an archive, not a bare executable", target, dl.name)
			}
			if !strings.HasSuffix(dl.name, ".zip") && !strings.HasSuffix(dl.name, ".tar.xz") {
				t.Errorf("%s: %q has no extractable suffix", target, dl.name)
			}
			if dl.checksums == "" {
				t.Errorf("%s: %q should be verified against a manifest", target, dl.name)
			}
		}
	}

	// The running platform must have a usable mapping, or the one-click
	// installer cannot work here at all.
	if _, err := ffmpegAssets(); err != nil {
		t.Errorf("no ffmpeg for the host %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
}

func TestInstallEnginesRejectsBadInput(t *testing.T) {
	r := testResolver(settings.Defaults())
	rep := &discardReporter{}
	if err := InstallEngines(nil, r, EngineSpec{}, rep); err == nil {
		t.Error("expected an error when no engine is selected")
	}
}

type discardReporter struct{}

func (d *discardReporter) Log(string, string)                   {}
func (d *discardReporter) Logf(string, string, ...any)          {}
func (d *discardReporter) Step(string, float64, string, string) {}

// Invalidate must keep the GPU verdict. Dropping it left the UI stuck on
// "checking GPU…" with nothing re-probing, because Report(ctx, false) just
// copies the cached value.
func TestInvalidateKeepsGPUVerdict(t *testing.T) {
	r := testResolver(settings.Defaults())
	r.mu.Lock()
	r.gpu = GPU{Checked: true, Available: true, Name: "RTX 4090", Torch: "2.5.0"}
	r.cache["ytdlp"] = Tool{ID: "ytdlp", Found: true}
	r.mu.Unlock()

	r.Invalidate()

	r.mu.Lock()
	gpu, tools := r.gpu, len(r.cache)
	r.mu.Unlock()

	if tools != 0 {
		t.Errorf("tool cache should be cleared, still has %d entries", tools)
	}
	if !gpu.Checked || !gpu.Available || gpu.Name != "RTX 4090" {
		t.Errorf("GPU verdict was lost: %+v", gpu)
	}

	// And the report must still carry it when not asked to re-probe.
	rep := Report{}
	r.mu.Lock()
	rep.GPU = r.gpu
	r.mu.Unlock()
	if !rep.GPU.Checked {
		t.Error("Report would show the GPU as unchecked")
	}
}

// InvalidateAll is for after an engine install, which can change which torch is
// present.
func TestInvalidateAllForgetsGPU(t *testing.T) {
	r := testResolver(settings.Defaults())
	r.mu.Lock()
	r.gpu = GPU{Checked: true, Available: true}
	r.mu.Unlock()

	r.InvalidateAll()

	r.mu.Lock()
	gpu := r.gpu
	r.mu.Unlock()
	if gpu.Checked {
		t.Errorf("expected the GPU probe to be forgotten, got %+v", gpu)
	}
}

// ReadyToRip is derived from the Required flag, not slice positions, so
// inserting a tool cannot silently change its meaning.
func TestReadyToRipIsDerivedFromRequired(t *testing.T) {
	tools := []Tool{
		{ID: ToolYtDlp, Required: true, Found: true},
		{ID: ToolFFmpeg, Required: true, Found: true},
		{ID: ToolJSRuntime, Required: false, Found: false},
		{ID: ToolPython, Required: false, Found: false},
	}
	ready := true
	for _, t2 := range tools {
		if t2.Required && !t2.Found {
			ready = false
		}
	}
	if !ready {
		t.Error("optional tools must not block readiness")
	}

	tools[1].Found = false
	ready = true
	for _, t2 := range tools {
		if t2.Required && !t2.Found {
			ready = false
		}
	}
	if ready {
		t.Error("a missing required tool must block readiness")
	}
}

func TestJSRuntimeNamesAreWhatYtDlpAccepts(t *testing.T) {
	// yt-dlp's --js-runtimes takes these names; the basename doubles as the name,
	// so the list must stay in sync with what the flag understands.
	want := map[string]bool{"deno": true, "node": true, "bun": true}
	if len(jsRuntimeNames) == 0 {
		t.Fatal("no JS runtimes configured")
	}
	for _, n := range jsRuntimeNames {
		if !want[n] {
			t.Errorf("%q is not a runtime yt-dlp accepts", n)
		}
	}
	// deno first: it is the only one yt-dlp auto-enables.
	if jsRuntimeNames[0] != "deno" {
		t.Errorf("expected deno first, got %q", jsRuntimeNames[0])
	}
}

func TestDenoAssetMatchPlatform(t *testing.T) {
	// Checked for every target rather than just the host, so a Linux CI run
	// still catches a broken macOS or Windows mapping.
	for _, tc := range []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "deno-x86_64-unknown-linux-gnu.zip"},
		{"linux", "arm64", "deno-aarch64-unknown-linux-gnu.zip"},
		{"darwin", "amd64", "deno-x86_64-apple-darwin.zip"},
		{"darwin", "arm64", "deno-aarch64-apple-darwin.zip"},
		{"windows", "amd64", "deno-x86_64-pc-windows-msvc.zip"},
		{"windows", "arm64", "deno-aarch64-pc-windows-msvc.zip"},
	} {
		target := tc.goos + "/" + tc.goarch
		got, err := denoAssetFor(tc.goos, tc.goarch)
		if err != nil {
			t.Errorf("%s: %v", target, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: asset = %q, want %q", target, got, tc.want)
		}
		// The release carries denort-* and libdenort-* under the same triples;
		// those are the embeddable runtime, not the CLI yt-dlp drives.
		if !strings.HasPrefix(got, "deno-") {
			t.Errorf("%s: asset %q must be the deno- CLI, not a denort build", target, got)
		}
	}

	if _, err := denoAssetFor("plan9", "mips"); err == nil {
		t.Error("expected an error for an unsupported platform")
	}
}

// unzip used to hardcode ffmpeg/ffprobe, so pointing it at any other archive
// quietly produced an empty directory and the caller failed later with a
// confusing "not found". Keep the allow-list honest, including the flattening
// that makes zip-slip impossible.
func TestUnzipExtractsOnlyWantedNames(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.zip")

	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{
		"deno",
		"LICENSE",
		"nested/deep/ffmpeg",
		"../../escape",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := filepath.Join(dir, "out")
	if err := paths.EnsureDir(dest); err != nil {
		t.Fatal(err)
	}
	if err := unzip(archive, dest, "deno", "ffmpeg", "escape"); err != nil {
		t.Fatalf("unzip: %v", err)
	}

	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(got)
	// "LICENSE" was not asked for. "nested/deep/ffmpeg" and "../../escape" both
	// land on their base name inside dest — the traversal never escapes.
	want := []string{"deno", "escape", "ffmpeg"}
	if !slices.Equal(got, want) {
		t.Errorf("extracted %v, want %v", got, want)
	}

	// Nothing wanted means nothing written, not everything written.
	empty := filepath.Join(dir, "empty")
	if err := paths.EnsureDir(empty); err != nil {
		t.Fatal(err)
	}
	if err := unzip(archive, empty); err != nil {
		t.Fatalf("unzip with no names: %v", err)
	}
	if entries, err := os.ReadDir(empty); err != nil || len(entries) != 0 {
		t.Errorf("expected nothing extracted, got %d entries (err %v)", len(entries), err)
	}
}

func TestArchMismatchGetsAnActionableHint(t *testing.T) {
	// The probe names the problem but not the cure, and "sm_52 is missing from
	// sm_75, sm_80, …" tells a user nothing about what to do next.
	mismatch := "GPU NVIDIA GeForce GTX 960 (sm_52) không nằm trong các kiến trúc torch hỗ trợ (sm_75, sm_80)"
	got := withArchHint(mismatch)
	// Points at the install panel rather than naming an index. Naming one was
	// wrong once: the hint said cu118 on the assumption that all current builds
	// had dropped Maxwell, but cu126 still carries sm_50 and is much newer.
	if !strings.Contains(got, "Phụ thuộc") {
		t.Errorf("hint should send the user to the install panel, got %q", got)
	}
	if strings.Contains(got, "cu118") || strings.Contains(got, "cu126") {
		t.Errorf("hint must not hardcode an index — it depends on the card: %q", got)
	}
	if !strings.HasPrefix(got, mismatch) {
		t.Errorf("hint must be appended, not replace the diagnosis: %q", got)
	}

	// Every other failure has a different cure, so none of them may collect it.
	for _, other := range []string{
		"",
		"torch không thấy CUDA",
		"chưa tìm thấy PyTorch",
		"không chạy được kiểm tra CUDA: exit status 1",
	} {
		if got := withArchHint(other); got != other {
			t.Errorf("withArchHint(%q) = %q, want it unchanged", other, got)
		}
	}
}

func TestVenvSeesSystemPackages(t *testing.T) {
	// Decides whether an explicit PyTorch flavour can be installed at all: in a
	// venv built with --system-site-packages, pip treats a torch in the user
	// site as satisfying the requirement and installs nothing into the venv.
	for _, tc := range []struct {
		name, cfg string
		want      bool
	}{
		{"true", "home = /usr/bin\ninclude-system-site-packages = true\nversion = 3.11.15\n", true},
		{"false", "home = /usr/bin\ninclude-system-site-packages = false\nversion = 3.11.15\n", false},
		{"viết hoa vẫn nhận", "include-system-site-packages = True\n", true},
		{"thừa khoảng trắng", "  include-system-site-packages   =   true  \n", true},
		{"không có khoá nào", "home = /usr/bin\nversion = 3.11.15\n", false},
		{"file rỗng", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("DEMUCS_STUDIO_HOME", home)
			if err := os.MkdirAll(paths.VenvDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := filepath.Join(paths.VenvDir(), "pyvenv.cfg")
			if err := os.WriteFile(cfg, []byte(tc.cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := venvSeesSystemPackages(); got != tc.want {
				t.Errorf("venvSeesSystemPackages() = %v, want %v", got, tc.want)
			}
		})
	}

	// No venv at all must read as isolated: guessing "true" would skip the
	// rebuild that a flavour switch depends on.
	t.Run("chưa có venv", func(t *testing.T) {
		t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())
		if venvSeesSystemPackages() {
			t.Error("a missing venv should not report system site-packages")
		}
	})
}

func TestCUDARuntimePrefixesCoverWhatTorchBundles(t *testing.T) {
	// The real reason this matters: nvidia-cudnn-cu11 and nvidia-cudnn-cu13
	// unpack into the same nvidia/cudnn/lib directory and overwrite each
	// other's libcudnn.so.9, so a leftover from another CUDA major shadows the
	// right one and every convolution fails. The whole set has to go on a
	// flavour switch, not just the torch packages.
	matches := func(name string) bool {
		lower := strings.ToLower(name)
		for _, p := range cudaRuntimePrefixes {
			if strings.HasPrefix(lower, p) {
				return true
			}
		}
		return false
	}

	// Names taken verbatim from a real venv after a cu118 and a cu130 install.
	for _, name := range []string{
		"nvidia-cudnn-cu11", "nvidia-cudnn-cu13", "nvidia-cuda-runtime",
		"nvidia-cublas-cu11", "nvidia-cufft", "nvidia-nvjitlink",
		"nvidia-cuda-nvrtc", "cuda-toolkit", "cuda-bindings", "cuda-pathfinder",
		"NVIDIA-cuDNN-cu11", // pip freeze preserves case; matching must not
	} {
		if !matches(name) {
			t.Errorf("%q should be treated as CUDA runtime and removed", name)
		}
	}

	// Nothing else may be swept up — losing these would break the engines.
	for _, name := range []string{
		"torch", "torchaudio", "demucs", "audio-separator", "numpy",
		"onnxruntime-gpu", "einops", "pip", "cudatoolkit-helper-lookalike",
	} {
		if name == "cudatoolkit-helper-lookalike" {
			// Starts with "cuda" but not "cuda-"/"cuda_": must not match.
			if matches(name) {
				t.Errorf("%q must not be swept up by the prefix match", name)
			}
			continue
		}
		if matches(name) {
			t.Errorf("%q must not be removed", name)
		}
	}
}

func TestLooksNumericVersion(t *testing.T) {
	// nvidia-smi answers "[Not Supported]" for a field an old driver cannot
	// report, exit code 0. Letting that through made it truthy in the webview,
	// which then stated with confidence that a working card was unsupported.
	for _, ok := range []string{"5.2", "580.173.02", "450", "12.0"} {
		if !looksNumericVersion(ok) {
			t.Errorf("%q should be accepted as a version", ok)
		}
	}
	for _, bad := range []string{"", "[Not Supported]", "N/A", "...", "5.2a", "unknown"} {
		if looksNumericVersion(bad) {
			t.Errorf("%q must be rejected, not passed on as a version", bad)
		}
	}
}

func TestDescribeCardOnlyFillsWhatIsMissing(t *testing.T) {
	// Values the torch probe supplied must win; smiCard is a fallback for the
	// gaps, notably the CPU-only-torch case where the probe reports no
	// capability at all.
	full := GPU{Name: "probe card", Capability: "8.6", Driver: "570.1"}
	if got := describeCard(context.Background(), full); got != full {
		t.Errorf("describeCard overwrote known values: %+v", got)
	}

	// With every field already set it must not shell out at all; a bogus PATH
	// would make nvidia-smi fail and blank the fields if it did.
	t.Setenv("PATH", t.TempDir())
	if got := describeCard(context.Background(), full); got != full {
		t.Errorf("describeCard changed a complete GPU: %+v", got)
	}
	// And with nothing to find, the gaps simply stay empty rather than becoming
	// junk the UI would render as a verdict.
	got := describeCard(context.Background(), GPU{Checked: true, Reason: "chưa tìm thấy PyTorch"})
	if got.Capability != "" || got.Driver != "" {
		t.Errorf("expected empty card fields with no nvidia-smi, got %+v", got)
	}
	if got.Reason != "chưa tìm thấy PyTorch" {
		t.Errorf("describeCard must not touch Reason, got %q", got.Reason)
	}
}

// The probe must name which accelerator it verified, because the app passes
// that string straight to the engines as the device. Resolving "auto" to a
// hardcoded "cuda" is what would send a Mac down a CUDA path its torch has no
// support for.
func TestGpuProbeHandlesBothBackends(t *testing.T) {
	// Structural checks on the probe source: it cannot be executed here without
	// a torch, but the two backends must both be reachable and both must run
	// the smoke test rather than trusting an is_available() flag.
	for _, want := range []string{
		`out["backend"] = "cuda"`,
		`out["backend"] = "mps"`,
		`smoke("cuda")`,
		`smoke("mps")`,
		// MPS built but unusable is its own diagnosis: an Intel Mac or a macOS
		// older than 12.3, neither of which is fixed by reinstalling.
		"mps.is_built()",
	} {
		if !strings.Contains(gpuProbe, want) {
			t.Errorf("gpuProbe is missing %q", want)
		}
	}
	// The smoke test has to be the same code for both, or one backend ends up
	// less thoroughly checked than the other — which is how a working
	// elementwise kernel once stood in for a convolution that failed.
	if strings.Count(gpuProbe, "conv1d") != 1 {
		t.Errorf("expected one shared conv1d smoke test, found %d", strings.Count(gpuProbe, "conv1d"))
	}
}

func TestAppleChipRejectsNonAppleOutput(t *testing.T) {
	// On this Linux box sysctl either is absent or prints an Intel/AMD string;
	// either way the name must stay empty rather than becoming junk the UI
	// renders as a GPU name.
	if got := appleChip(context.Background()); got != "" && !strings.HasPrefix(got, "Apple ") {
		t.Errorf("appleChip returned %q, want empty or an Apple string", got)
	}
}

// Importing torch costs seconds and hundreds of megabytes, and two callers
// arrive at launch within that window: the startup warm-up goroutine and the
// frontend's SuggestedAccel. Checking the cache is not enough on its own — the
// check happens before the probe, so both used to find it unchecked and each
// forked its own interpreter.
func TestDetectGPUProbesOnceForConcurrentCallers(t *testing.T) {
	var probes atomic.Int32
	release := make(chan struct{})
	r := NewResolver(func() settings.Settings { return settings.Defaults() })
	r.probeFn = func(context.Context) GPU {
		probes.Add(1)
		<-release // hold the probe open so every caller piles up behind it
		return GPU{Checked: true, Available: true, Backend: "cuda", Name: "GTX 960"}
	}

	const callers = 8
	got := make(chan GPU, callers)
	var started sync.WaitGroup
	started.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			started.Done()
			got <- r.DetectGPU(context.Background())
		}()
	}
	started.Wait()
	// Every caller is inside DetectGPU now; let the single probe finish.
	close(release)

	for i := 0; i < callers; i++ {
		if g := <-got; g.Backend != "cuda" || g.Name != "GTX 960" {
			t.Errorf("caller %d nhận %+v, muốn kết quả của probe duy nhất", i, g)
		}
	}
	if n := probes.Load(); n != 1 {
		t.Errorf("%d lần probe cho %d caller đồng thời, muốn đúng 1", n, callers)
	}

	// And the answer is cached afterwards, so a later caller adds nothing.
	r.DetectGPU(context.Background())
	if n := probes.Load(); n != 1 {
		t.Errorf("caller sau khi đã cache lại probe thêm: %d", n)
	}
}

// InvalidateAll exists to force a re-probe after an engine install, so the
// dedup must not turn into a permanent cache.
func TestDetectGPUReprobesAfterInvalidateAll(t *testing.T) {
	var probes atomic.Int32
	r := NewResolver(func() settings.Settings { return settings.Defaults() })
	r.probeFn = func(context.Context) GPU {
		probes.Add(1)
		return GPU{Checked: true, Reason: "chưa tìm thấy PyTorch"}
	}
	r.DetectGPU(context.Background())
	r.InvalidateAll()
	r.DetectGPU(context.Background())
	if n := probes.Load(); n != 2 {
		t.Errorf("probe %d lần quanh InvalidateAll, muốn 2", n)
	}
}

// A caller that gives up must not be left hanging on someone else's probe, and
// must not poison the cache with its own cancellation.
func TestDetectGPUWaiterHonoursContextCancel(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	r := NewResolver(func() settings.Settings { return settings.Defaults() })
	r.probeFn = func(context.Context) GPU {
		<-release
		return GPU{Checked: true, Available: true, Backend: "cuda"}
	}

	holder := make(chan struct{})
	go func() { defer close(holder); r.DetectGPU(context.Background()) }()

	// Wait until the probe is genuinely in flight, then cancel a second caller.
	for {
		r.mu.Lock()
		inFlight := r.gpuInFlight != nil
		r.mu.Unlock()
		if inFlight {
			break
		}
		runtime.Gosched()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if g := r.DetectGPU(ctx); g.Available {
		t.Errorf("caller bị huỷ nhận %+v, không được báo có GPU", g)
	}
	r.mu.Lock()
	cached := r.gpu
	r.mu.Unlock()
	if cached.Checked {
		t.Errorf("huỷ đã ghi vào cache: %+v", cached)
	}
}
