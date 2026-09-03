package deps

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"demucs-studio/internal/netfetch"
	"demucs-studio/internal/paths"
	"demucs-studio/internal/proc"
)

// Reporter is the subset of the event bus the installers need.
type Reporter interface {
	Log(level, text string)
	Logf(level, format string, args ...any)
	Step(phase string, fraction float64, label, detail string)
}

const (
	ytDlpRelease  = "https://github.com/yt-dlp/yt-dlp/releases/latest/download/"
	ffmpegRelease = "https://github.com/yt-dlp/FFmpeg-Builds/releases/download/latest/"

	// Both projects publish a sha256sum-format manifest next to their assets.
	ytDlpChecksums  = ytDlpRelease + "SHA2-256SUMS"
	ffmpegChecksums = ffmpegRelease + "checksums.sha256"

	// deno publishes one zip per platform under a stable name, plus a
	// sha256sum-format file beside each asset. That format matches the single
	// manifest the other two projects publish, so publishedChecksum reads it
	// unchanged — only the URL differs.
	denoRelease = "https://github.com/denoland/deno/releases/latest/download/"

	// yt-dlp's FFmpeg-Builds publishes no macOS asset at all, so darwin pulls
	// the static builds from eugeneware/ffmpeg-static instead. That project
	// uploads the executables bare rather than inside an archive, and ships no
	// checksum manifest — there, netfetch's Content-Length check is all we get.
	ffmpegMacRelease = "https://github.com/eugeneware/ffmpeg-static/releases/latest/download/"
)

// publishedChecksum looks up an asset's SHA-256 in a release manifest. A
// failure here is not fatal: the download still proceeds, because refusing to
// install yt-dlp when GitHub is having a bad day would be worse than installing
// it with only the Content-Length check that netfetch always applies.
func publishedChecksum(ctx context.Context, manifestURL, asset string, rep Reporter) string {
	sum, err := netfetch.ChecksumList(ctx, manifestURL, asset)
	if err != nil {
		rep.Logf(LevelWarn, "Không lấy được checksum công bố cho %s (%v) — vẫn tải, nhưng chỉ kiểm được dung lượng.", asset, err)
		return ""
	}
	rep.Logf(LevelInfo, "Checksum công bố cho %s: %s", asset, sum)
	return sum
}

// ytDlpAsset picks the standalone yt-dlp build for this platform.
func ytDlpAsset() (asset, dest string, err error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64", "windows/386":
		return "yt-dlp.exe", "yt-dlp.exe", nil
	case "windows/arm64":
		return "yt-dlp.exe", "yt-dlp.exe", nil
	case "linux/amd64":
		return "yt-dlp_linux", "yt-dlp", nil
	case "linux/arm64":
		return "yt-dlp_linux_aarch64", "yt-dlp", nil
	case "linux/arm":
		return "yt-dlp_linux_armv7l", "yt-dlp", nil
	case "darwin/amd64", "darwin/arm64":
		return "yt-dlp_macos", "yt-dlp", nil
	}
	return "", "", fmt.Errorf("không có bản yt-dlp dựng sẵn cho %s/%s", runtime.GOOS, runtime.GOARCH)
}

// denoAsset picks the deno build for this platform.
func denoAsset() (string, error) { return denoAssetFor(runtime.GOOS, runtime.GOARCH) }

// denoAssetFor is the platform-parameterised half of denoAsset, so tests can
// check every target's mapping from whichever host they run on.
func denoAssetFor(goos, goarch string) (string, error) {
	var triple string
	switch goos + "/" + goarch {
	case "linux/amd64":
		triple = "x86_64-unknown-linux-gnu"
	case "linux/arm64":
		triple = "aarch64-unknown-linux-gnu"
	case "darwin/amd64":
		triple = "x86_64-apple-darwin"
	case "darwin/arm64":
		triple = "aarch64-apple-darwin"
	case "windows/amd64":
		triple = "x86_64-pc-windows-msvc"
	case "windows/arm64":
		triple = "aarch64-pc-windows-msvc"
	default:
		return "", fmt.Errorf("không có bản deno dựng sẵn cho %s/%s", goos, goarch)
	}
	// The same release also carries denort-* and libdenort-* for every triple,
	// which are the embeddable runtime rather than the CLI. The prefix has to be
	// exact or the download silently yields something that cannot run scripts.
	return "deno-" + triple + ".zip", nil
}

// InstallDeno downloads the deno runtime into AppDir/bin.
//
// yt-dlp needs a JavaScript runtime to solve YouTube's challenges, and accepts
// deno, node or bun. deno is the one worth installing unattended: it is the
// only one that ships as a single self-contained executable, so installing it
// is a download and a copy rather than a package manager or a directory tree.
// It is also the one yt-dlp auto-enables.
func InstallDeno(ctx context.Context, rep Reporter) error {
	asset, err := denoAsset()
	if err != nil {
		return err
	}
	if err := paths.EnsureDir(paths.BinDir()); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "deno-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	url := denoRelease + asset
	local := filepath.Join(work, asset)
	rep.Logf(LevelInfo, "Tải deno: %s", url)
	sum := publishedChecksum(ctx, url+".sha256sum", asset, rep)
	err = netfetch.Get(ctx, url, local, sum, func(done, total int64) {
		rep.Step("install", fraction(done, total)*0.9, "Đang tải deno", byteRange(done, total))
	})
	if err != nil {
		return err
	}

	rep.Step("install", 0.92, "Đang giải nén deno", "")
	extracted := filepath.Join(work, "x")
	if err := paths.EnsureDir(extracted); err != nil {
		return err
	}
	name := paths.Exe("deno")
	if err := unzip(local, extracted, name); err != nil {
		return err
	}

	// unzip flattens, so a match always lands directly in extracted regardless
	// of where it sat in the archive.
	src := filepath.Join(extracted, name)
	if !isExecutable(src) {
		return errors.New("không tìm thấy deno trong gói vừa tải")
	}

	dest := filepath.Join(paths.BinDir(), name)
	if err := copyFile(src, dest, 0o755); err != nil {
		return err
	}
	adhocSign(ctx, dest, rep)
	rep.Step("install", 1, "Hoàn tất", "")
	rep.Logf(LevelInfo, "deno đã cài vào %s", dest)
	return nil
}

// ffmpegDownload is one file InstallFFmpeg has to fetch.
type ffmpegDownload struct {
	url string
	// name is the asset's file name, used both to store it while downloading
	// and to look it up in the checksum manifest.
	name string
	// exe is set when url points straight at an executable, and holds the name
	// to install it under. Empty means the download is an archive to unpack.
	exe string
	// checksums is the manifest to verify against, empty when the project
	// publishes none.
	checksums string
}

// ffmpegAssets picks the static ffmpeg build(s) for this platform.
func ffmpegAssets() ([]ffmpegDownload, error) {
	return ffmpegAssetsFor(runtime.GOOS, runtime.GOARCH)
}

// ffmpegAssetsFor is the platform-parameterised half of ffmpegAssets, so tests
// can check every target's mapping from whichever host they run on. It returns
// one archive holding both executables everywhere except macOS, where the two
// bare binaries have to be fetched separately.
func ffmpegAssetsFor(goos, goarch string) ([]ffmpegDownload, error) {
	if goos == "darwin" {
		// eugeneware/ffmpeg-static names its assets with Node's architecture
		// strings, where Go's "amd64" is "x64".
		arch := goarch
		if arch == "amd64" {
			arch = "x64"
		}
		if arch != "x64" && arch != "arm64" {
			return nil, fmt.Errorf("không có bản ffmpeg dựng sẵn cho %s/%s", goos, goarch)
		}
		out := make([]ffmpegDownload, 0, 2)
		for _, tool := range []string{"ffmpeg", "ffprobe"} {
			asset := tool + "-darwin-" + arch
			out = append(out, ffmpegDownload{url: ffmpegMacRelease + asset, name: asset, exe: tool})
		}
		return out, nil
	}

	var asset string
	switch goos + "/" + goarch {
	case "windows/amd64":
		asset = "ffmpeg-master-latest-win64-gpl.zip"
	case "windows/arm64":
		asset = "ffmpeg-master-latest-winarm64-gpl.zip"
	case "linux/amd64":
		asset = "ffmpeg-master-latest-linux64-gpl.tar.xz"
	case "linux/arm64":
		asset = "ffmpeg-master-latest-linuxarm64-gpl.tar.xz"
	default:
		return nil, fmt.Errorf("không có bản ffmpeg dựng sẵn cho %s/%s", goos, goarch)
	}
	return []ffmpegDownload{{url: ffmpegRelease + asset, name: asset, checksums: ffmpegChecksums}}, nil
}

// InstallYtDlp downloads the standalone yt-dlp binary into AppDir/bin.
func InstallYtDlp(ctx context.Context, rep Reporter) error {
	asset, destName, err := ytDlpAsset()
	if err != nil {
		return err
	}
	if err := paths.EnsureDir(paths.BinDir()); err != nil {
		return err
	}
	dest := filepath.Join(paths.BinDir(), destName)
	url := ytDlpRelease + asset

	rep.Logf(LevelInfo, "Tải yt-dlp: %s", url)
	sum := publishedChecksum(ctx, ytDlpChecksums, asset, rep)
	err = netfetch.Get(ctx, url, dest, sum, func(done, total int64) {
		rep.Step("install", fraction(done, total), "Đang tải yt-dlp", byteRange(done, total))
	})
	if err != nil {
		return err
	}
	if err := os.Chmod(dest, 0o755); err != nil {
		return err
	}
	rep.Logf(LevelInfo, "yt-dlp đã cài vào %s", dest)
	return nil
}

// InstallFFmpeg downloads a static ffmpeg/ffprobe pair into AppDir/bin.
func InstallFFmpeg(ctx context.Context, rep Reporter) error {
	downloads, err := ffmpegAssets()
	if err != nil {
		return err
	}
	if err := paths.EnsureDir(paths.BinDir()); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "ffmpeg-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	found := 0
	// Downloading is the slow part, so it owns the 0..0.85 progress band; each
	// asset gets an equal slice of it.
	span := 0.85 / float64(len(downloads))
	for i, dl := range downloads {
		base := span * float64(i)
		local := filepath.Join(work, dl.name)
		rep.Logf(LevelInfo, "Tải ffmpeg: %s", dl.url)

		sum := ""
		if dl.checksums != "" {
			sum = publishedChecksum(ctx, dl.checksums, dl.name, rep)
		}
		err := netfetch.Get(ctx, dl.url, local, sum, func(done, total int64) {
			rep.Step("install", base+fraction(done, total)*span, "Đang tải ffmpeg", byteRange(done, total))
		})
		if err != nil {
			return err
		}

		n, err := installFFmpegAsset(ctx, dl, local, work, rep)
		if err != nil {
			return err
		}
		found += n
	}
	if found == 0 {
		return errors.New("không tìm thấy ffmpeg trong gói vừa tải")
	}
	return nil
}

// installFFmpegAsset puts the executables from one finished download into
// AppDir/bin and reports how many it installed.
func installFFmpegAsset(ctx context.Context, dl ffmpegDownload, local, work string, rep Reporter) (int, error) {
	if dl.exe != "" {
		dest := filepath.Join(paths.BinDir(), paths.Exe(dl.exe))
		if err := copyFile(local, dest, 0o755); err != nil {
			return 0, err
		}
		adhocSign(ctx, dest, rep)
		rep.Logf(LevelInfo, "Đã cài %s", dest)
		return 1, nil
	}

	rep.Step("install", 0.9, "Đang giải nén ffmpeg", "")
	extracted := filepath.Join(work, "x")
	if err := paths.EnsureDir(extracted); err != nil {
		return 0, err
	}
	if strings.HasSuffix(dl.name, ".zip") {
		if err := unzip(local, extracted, paths.Exe("ffmpeg"), paths.Exe("ffprobe")); err != nil {
			return 0, err
		}
	} else if err := untarXZ(ctx, local, extracted); err != nil {
		return 0, err
	}

	wanted := map[string]bool{paths.Exe("ffmpeg"): true, paths.Exe("ffprobe"): true}
	found := 0
	err := filepath.WalkDir(extracted, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !wanted[d.Name()] {
			return nil
		}
		dest := filepath.Join(paths.BinDir(), d.Name())
		if err := copyFile(p, dest, 0o755); err != nil {
			return err
		}
		found++
		rep.Logf(LevelInfo, "Đã cài %s", dest)
		return nil
	})
	return found, err
}

// adhocSign re-signs a freshly downloaded binary on macOS. Apple Silicon
// refuses to exec an arm64 Mach-O carrying no signature at all — the process is
// SIGKILLed before it reaches main — and third-party static builds are not
// reliably signed. An ad-hoc signature is enough to satisfy the kernel and is
// free to apply. A failure is only logged: codesign may be absent on a machine
// without the Xcode command line tools, and the binary often runs anyway, so
// turning this into a hard error would block installs that would have worked.
func adhocSign(ctx context.Context, path string, rep Reporter) {
	if runtime.GOOS != "darwin" {
		return
	}
	if _, ok := probe(ctx, "codesign", "--force", "--sign", "-", path); !ok {
		rep.Logf(LevelWarn, "Không ký lại được %s. Nếu macOS chặn, chạy tay: codesign --force --sign - %s", path, path)
	}
}

// EngineSpec describes a Python engine installation request.
type EngineSpec struct {
	// Engines to install: "demucs", "audioSeparator", or both.
	Engines []string `json:"engines"`
	// Accel: "cuda" installs CUDA torch wheels, "cpu" the CPU-only ones,
	// "reuse" creates the venv with --system-site-packages and keeps whatever
	// torch the host Python already has (fast, no multi-GB download).
	Accel string `json:"accel"`
	// CudaTag selects the PyTorch wheel index, e.g. "cu126", "cu128".
	CudaTag string `json:"cudaTag"`
}

// withResidentEngines adds the engines already installed in the venv to the
// requested set, so a rebuild does not silently uninstall one of them.
//
// The install panel offers a button per missing engine and sends exactly one
// engine id, so without this a flavour switch performed from the demucs row
// removed audio-separator and vice versa — leaving the deps tab reporting an
// engine the user never asked to lose.
// Takes the resolved tools rather than the Resolver so it stays a pure
// decision: resolving an engine shells out to it for a version string, which a
// test has no business doing.
func withResidentEngines(requested []string, resident map[string]Tool) []string {
	out := append([]string{}, requested...)
	// Iterated in a fixed order; ranging a map would shuffle the pip calls
	// between runs and make the install log non-reproducible.
	for _, id := range []string{ToolDemucs, ToolAudioSeparator} {
		tool, ok := resident[id]
		// Only what lives in the venv is about to be destroyed; an engine on
		// the system PATH survives and must not be reinstalled into the venv.
		if !ok || !tool.Found || !strings.HasPrefix(tool.Path, paths.VenvDir()) {
			continue
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// interpreterOutsideVenv finds a Python that does not live in the managed venv,
// which is what a rebuild has to be driven by.
//
// The venv records the interpreter that created it in pyvenv.cfg's `executable`
// key, and that is the best answer: it is known to be able to build this venv.
// Failing that — Python upgraded or removed since — fall back to scanning the
// resolver's candidates and skipping anything inside the venv.
func interpreterOutsideVenv(r *Resolver, ctx context.Context) ([]string, error) {
	if exe := venvCreator(); exe != "" && isExecutable(exe) {
		return []string{exe}, nil
	}
	for _, argv := range r.pythonCandidates() {
		resolved := resolvePython(argv)
		if len(resolved) == 0 || strings.HasPrefix(resolved[0], paths.VenvDir()) {
			continue
		}
		if v, ok := probeArgv(ctx, resolved, "--version"); ok && looksLikePython3(v) {
			return resolved, nil
		}
	}
	return nil, errors.New("cần một bản Python 3 ngoài môi trường của app để tạo lại nó — hãy cài Python 3.10+ rồi thử lại")
}

// venvCreator reads the interpreter path `venv` recorded when it built the
// managed environment.
func venvCreator() string {
	raw, err := os.ReadFile(filepath.Join(paths.VenvDir(), "pyvenv.cfg"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "executable" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// cudaRuntimePrefixes are the distribution-name prefixes PyTorch's own CUDA
// dependencies use. Everything matching is part of the bundled toolkit and
// belongs to exactly one CUDA major version.
var cudaRuntimePrefixes = []string{"nvidia-", "nvidia_", "cuda-", "cuda_"}

// installedCUDARuntime lists the CUDA runtime distributions present in the
// venv, so a flavour switch can remove the whole set rather than leave halves
// of two CUDA majors fighting over the same shared-library filenames.
//
// pip has no wildcard uninstall, hence reading the list first. A failure here
// is not fatal: the caller only loses the cleanup, and reinstalling torch on
// top is still an improvement over doing nothing.
func installedCUDARuntime(ctx context.Context, venvPy string) []string {
	out, err := proc.Output(ctx, proc.Options{
		Bin:  venvPy,
		Args: []string{"-m", "pip", "list", "--format=freeze"},
	})
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name, _, ok := strings.Cut(strings.TrimSpace(line), "==")
		if !ok {
			continue
		}
		lower := strings.ToLower(name)
		for _, prefix := range cudaRuntimePrefixes {
			if strings.HasPrefix(lower, prefix) {
				names = append(names, name)
				break
			}
		}
	}
	return names
}

// venvSeesSystemPackages reports whether the managed venv was created with
// --system-site-packages, by reading the flag `venv` records in pyvenv.cfg.
// A missing or unreadable file reads as false: that is the isolated default,
// and guessing "true" would skip a rebuild that the caller needs.
func venvSeesSystemPackages() bool {
	raw, err := os.ReadFile(filepath.Join(paths.VenvDir(), "pyvenv.cfg"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "include-system-site-packages" {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(value), "true")
	}
	return false
}

// InstallEngines creates (or reuses) the managed virtual environment and pip
// installs the requested engines into it.
func InstallEngines(ctx context.Context, r *Resolver, spec EngineSpec, rep Reporter) error {
	if len(spec.Engines) == 0 {
		return errors.New("chưa chọn engine nào để cài")
	}
	host := r.Python(ctx)
	if !host.Found {
		return errors.New("không tìm thấy Python 3. Hãy cài Python 3.10+ rồi thử lại")
	}
	// cu126 rather than the cu124 this used to default to: PyTorch stopped
	// publishing new wheels to the cu124 index, so on Python 3.13 it resolves to
	// torch 2.6 while cu126 reaches 2.14. Both need only a 12.x driver, so this
	// costs no compatibility. Newer indexes are not a safe default — cu130 needs
	// a 580+ driver, and a mismatch there fails at runtime, not at install time.
	if spec.CudaTag == "" {
		spec.CudaTag = "cu126"
	}
	if spec.Accel == "" {
		spec.Accel = "reuse"
	}

	// host.Argv may carry arguments (the Windows `py -3` launcher), so the venv
	// command has to be assembled from the whole prefix.
	base := host.Argv
	if len(base) == 0 {
		base = []string{host.Path}
	}
	inVenv := strings.HasPrefix(host.Path, paths.VenvDir())

	// An explicit flavour needs an isolated venv. With
	// include-system-site-packages pip counts a torch in the user site as
	// satisfying the requirement and installs nothing into the venv at all, so
	// the flavour switch silently does nothing.
	//
	// "reuse" deliberately never triggers a rebuild. It used to: a venv built
	// for an explicit flavour is not system-site, so "reuse" looked like a kind
	// change and wiped it — and because the reuse path installs no torch, the
	// working CUDA build was replaced by whatever pip resolved as a dependency,
	// which is the CPU wheel. Reuse means reuse; if the venv cannot see a host
	// torch, refuseEmptyReuse below says so instead of degrading silently.
	needsRebuild := inVenv && spec.Accel != "reuse" && venvSeesSystemPackages()

	if inVenv && !needsRebuild {
		rep.Log(LevelInfo, "Dùng lại môi trường Python sẵn có của app")
	} else {
		if needsRebuild {
			// Rebuilding destroys every package in the venv, including engines
			// this call was not asked to install, so they have to be reinstalled
			// alongside. Collected before the wipe, while they are still visible.
			spec.Engines = withResidentEngines(spec.Engines, map[string]Tool{
				ToolDemucs:         r.Demucs(ctx),
				ToolAudioSeparator: r.AudioSeparator(ctx),
			})
			rep.Log(LevelWarn, "Môi trường Python hiện tại không phù hợp với lựa chọn PyTorch — sẽ tạo lại từ đầu.")

			// Removed from Go rather than with `venv --clear`, and recreated by
			// an interpreter from outside: needsRebuild implies the resolved
			// host python IS the venv python, so --clear would have asked it to
			// delete its own executable. Windows refuses outright.
			outside, err := interpreterOutsideVenv(r, ctx)
			if err != nil {
				return err
			}
			base = outside
			if err := os.RemoveAll(paths.VenvDir()); err != nil {
				return fmt.Errorf("không xoá được môi trường Python cũ: %w", err)
			}
		}
		args := append(append([]string{}, base[1:]...), "-m", "venv")
		if spec.Accel == "reuse" {
			args = append(args, "--system-site-packages")
		}
		args = append(args, paths.VenvDir())
		rep.Step("install", 0.02, "Tạo môi trường Python", paths.VenvDir())
		rep.Logf(LevelInfo, "%s %s", base[0], strings.Join(args, " "))
		if err := stream(ctx, rep, base[0], args...); err != nil {
			return fmt.Errorf("tạo venv thất bại: %w", err)
		}
	}

	// "reuse" installs no torch, so it only works when one is already visible.
	// Without this check pip resolved torch as a dependency of the engines,
	// which means the PyPI default — the CPU wheel — and the user ended up on
	// CPU having chosen the option that promised to keep what they had.
	if spec.Accel == "reuse" && !canImport(ctx, []string{paths.VenvBin("python")}, "torch") {
		return errors.New(`chọn "Dùng lại bản đã có" nhưng không thấy PyTorch nào dùng được — ` +
			`hãy chọn "Tải bản CUDA (GPU)" hoặc "Tải bản CPU"`)
	}

	venvPy := paths.VenvBin("python")
	if !isExecutable(venvPy) {
		return fmt.Errorf("không tìm thấy python trong venv: %s", venvPy)
	}

	pip := func(step float64, label string, args ...string) error {
		rep.Step("install", step, label, "")
		full := append([]string{"-m", "pip", "install", "--upgrade", "--no-input"}, args...)
		rep.Logf(LevelInfo, "pip install %s", strings.Join(args, " "))
		return stream(ctx, rep, venvPy, full...)
	}

	if err := pip(0.05, "Cập nhật pip", "pip", "wheel"); err != nil {
		return err
	}

	if spec.Accel != "reuse" {
		index := "https://download.pytorch.org/whl/cpu"
		if spec.Accel == "cuda" {
			index = "https://download.pytorch.org/whl/" + spec.CudaTag
		}
		// Remove the family before installing it. pip's --upgrade only ever
		// moves forward, so switching to an older index — exactly what a
		// Maxwell or Pascal card needs, since recent CUDA builds dropped those
		// architectures — was a silent no-op: cu118 tops out at torch 2.6 while
		// an installed cu130 build is 2.12, so pip answered "already satisfied"
		// and the user was left on a torch with no kernels for their GPU.
		//
		// torchvision and torchcodec go too even though they are not installed
		// from here: they are compiled against one exact torch, and leaving a
		// mismatched pair behind breaks the import rather than the install.
		rep.Step("install", 0.1, "Xoá PyTorch cũ", "")
		doomed := []string{"torch", "torchaudio", "torchvision", "torchcodec"}
		// The bundled CUDA runtime has to go with it. Packages like
		// nvidia-cudnn-cu11 and nvidia-cudnn-cu13 unpack into the *same*
		// directory (nvidia/cudnn/lib) and overwrite each other's
		// libcudnn.so.9, so a leftover from another CUDA major silently
		// shadows the right one. Seen exactly that: torch 2.7.1+cu118 loading
		// cuDNN 9.24 from an orphaned cu13 package, and every convolution
		// failing with "GET was unable to find an engine to execute this
		// computation" — after the GPU had already tested fine, because a
		// plain elementwise kernel never touches cuDNN.
		doomed = append(doomed, installedCUDARuntime(ctx, venvPy)...)
		if err := stream(ctx, rep, venvPy,
			append([]string{"-m", "pip", "uninstall", "-y"}, doomed...)...); err != nil {
			// Nothing to remove is the normal first-install case, and pip exits
			// non-zero on some versions for that. The install below is what
			// actually has to succeed.
			rep.Logf(LevelInfo, "Không có PyTorch cũ để xoá (%v)", err)
		}

		// torchvision belongs in this list even though nothing here imports it.
		// audio-separator pulls it in transitively (via onnx2torch), and if it
		// is missing at that point pip resolves it from PyPI, picks the newest
		// build, and drags that build's matching torch along — silently undoing
		// the flavour just installed. Observed exactly that: a freshly
		// installed torch 2.7.1+cu118 replaced by 2.14.0+cu130, leaving the
		// same "no kernels for this GPU" state the install was meant to fix.
		rep.Logf(LevelWarn, "Đang tải PyTorch (%s) — có thể vài GB, vui lòng chờ.", spec.Accel)
		if err := pip(0.15, "Cài PyTorch ("+spec.Accel+")",
			"--index-url", index, "torch", "torchaudio", "torchvision"); err != nil {
			return err
		}
	}

	step := 0.6
	for _, e := range spec.Engines {
		switch e {
		case ToolDemucs:
			if err := pip(step, "Cài Demucs", "demucs"); err != nil {
				return err
			}
		case ToolAudioSeparator:
			extra := "audio-separator[cpu]"
			if spec.Accel == "cuda" {
				extra = "audio-separator[gpu]"
			} else if spec.Accel == "reuse" {
				// Let it use the torch already visible via system-site-packages.
				extra = "audio-separator"
			}
			if err := pip(step, "Cài audio-separator", extra); err != nil {
				return err
			}
		default:
			return fmt.Errorf("engine không hợp lệ: %s", e)
		}
		step += 0.15
	}

	r.InvalidateAll()
	rep.Step("install", 1, "Hoàn tất", "")
	rep.Log(LevelInfo, "Cài đặt engine xong.")
	return nil
}

// UpdateYtDlp re-downloads yt-dlp. YouTube changes often enough that a stale
// yt-dlp is the most common cause of download failures.
func UpdateYtDlp(ctx context.Context, r *Resolver, rep Reporter) error {
	t := r.YtDlp(ctx)
	// A yt-dlp we installed ourselves is replaced wholesale; a system one is
	// asked to self-update so we do not shadow the packaged copy.
	if t.Found && t.Source != "app" && t.Source != "bundled" {
		rep.Log(LevelInfo, "Chạy yt-dlp --update")
		if err := stream(ctx, rep, t.Path, "--update"); err == nil {
			r.Invalidate()
			return nil
		}
		rep.Log(LevelWarn, "Self-update thất bại, tải lại bản standalone.")
	}
	if err := InstallYtDlp(ctx, rep); err != nil {
		return err
	}
	r.Invalidate()
	return nil
}

func stream(ctx context.Context, rep Reporter, bin string, args ...string) error {
	return proc.Run(ctx, proc.Options{
		Bin:      bin,
		Args:     args,
		ExtraEnv: []string{"PYTHONUNBUFFERED=1", "PIP_DISABLE_PIP_VERSION_CHECK=1"},
		OnLine: func(streamName, line string) {
			level := LevelInfo
			if streamName == "stderr" {
				level = LevelWarn
			}
			rep.Log(level, line)
		},
	})
}

func untarXZ(ctx context.Context, archive, dest string) error {
	// Streaming xz is not in the standard library; the platforms where we ship
	// .tar.xz (Linux) always have tar with xz support.
	if err := proc.Run(ctx, proc.Options{
		Bin:  "tar",
		Args: []string{"-xJf", archive, "-C", dest},
	}); err != nil {
		return fmt.Errorf("giải nén %s thất bại (cần lệnh tar): %w", filepath.Base(archive), err)
	}
	return nil
}

// unzip extracts the entries whose base name is in wanted, flattening the
// archive's directory layout into dest.
//
// Flattening does double duty: the caller does not have to know where in the
// archive an executable lives, and no entry name ever reaches the filesystem —
// only its base name, matched against wanted — which is what makes zip-slip on
// a crafted path impossible.
//
// wanted is a parameter rather than a hardcoded pair: it used to be fixed to
// ffmpeg/ffprobe, which silently produced an empty directory for any other
// archive.
func unzip(archive, dest string, wanted ...string) error {
	want := make(map[string]bool, len(wanted))
	for _, n := range wanted {
		want[n] = true
	}
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.Base(f.Name)
		if !want[name] {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out := filepath.Join(dest, name)
		w, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(w, rc)
		rc.Close()
		closeErr := w.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func copyFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Replacing a running binary fails on Windows; remove first.
	_ = os.Remove(dest)
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, mode)
}

func fraction(done, total int64) float64 {
	if total <= 0 {
		return -1
	}
	return float64(done) / float64(total)
}

func byteRange(done, total int64) string {
	if total <= 0 {
		return humanBytes(done)
	}
	return humanBytes(done) + " / " + humanBytes(total)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, u := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

// Log level aliases so callers do not need to import the bus package.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)
