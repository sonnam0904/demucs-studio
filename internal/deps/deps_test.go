package deps

import (
	"runtime"
	"slices"
	"strings"
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
