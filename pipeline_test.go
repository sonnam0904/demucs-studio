package main

// End-to-end coverage of the real pipeline: yt-dlp downloads a short public
// video, then each engine separates it with the model the UI would use.
//
// It talks to YouTube and burns real GPU/CPU time, so it is opt-in:
//
//	DEMUCS_STUDIO_E2E=1 go test -run TestPipeline -timeout 60m -v ./...
//
// Override the source with DEMUCS_STUDIO_E2E_URL.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"demucs-studio/internal/deps"
	"demucs-studio/internal/engine"
	"demucs-studio/internal/engine/demucs"
	"demucs-studio/internal/engine/roformer"
	"demucs-studio/internal/settings"
	"demucs-studio/internal/ytdl"
)

// "Me at the zoo" — 19 seconds, always available, and it has a voice over
// ambient noise, so a vocal stem is actually meaningful.
const defaultTestURL = "https://www.youtube.com/watch?v=jNQXAC9IVRw"

// testReporter forwards engine output into the test log.
type testReporter struct {
	t        *testing.T
	lastStep time.Time
}

func (r *testReporter) Log(level, text string) {
	if level == "debug" {
		return
	}
	r.t.Logf("[%s] %s", level, text)
}

func (r *testReporter) Logf(level, format string, args ...any) {
	r.t.Logf("["+level+"] "+format, args...)
}

func (r *testReporter) Step(phase string, fraction float64, label, detail string) {
	// Throttle: a tqdm bar would otherwise produce thousands of lines.
	if time.Since(r.lastStep) < 2*time.Second && fraction < 1 {
		return
	}
	r.lastStep = time.Now()
	r.t.Logf("  %s %5.1f%% %s %s", phase, fraction*100, label, detail)
}

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("DEMUCS_STUDIO_E2E") != "1" {
		t.Skip("set DEMUCS_STUDIO_E2E=1 to run the end-to-end pipeline test")
	}
}

func testURL() string {
	if u := os.Getenv("DEMUCS_STUDIO_E2E_URL"); u != "" {
		return u
	}
	return defaultTestURL
}

func newResolver() *deps.Resolver {
	return deps.NewResolver(func() settings.Settings { return settings.Defaults() })
}

// TestUpdateYtDlp exercises the recovery path behind the UI's "Cập nhật yt-dlp"
// button. It matters because a stale yt-dlp is the single most common cause of
// download failures: YouTube ships new anti-bot checks every few weeks, and an
// out-of-date yt-dlp surfaces that as "HTTP Error 403: Forbidden" mid-download.
//
// A pip-installed yt-dlp refuses `--update` ("Use that to update"), so the code
// must fall back to fetching the standalone build into the app's own bin dir,
// which the resolver prefers over PATH.
func TestUpdateYtDlp(t *testing.T) {
	requireE2E(t)
	ctx := context.Background()
	r := &testReporter{t: t}
	resolver := newResolver()

	before := resolver.YtDlp(ctx)
	t.Logf("before: %s (%s) at %s", before.Version, before.Source, before.Path)

	if err := deps.UpdateYtDlp(ctx, resolver, r); err != nil {
		t.Fatalf("UpdateYtDlp: %v", err)
	}

	after := resolver.YtDlp(ctx)
	if !after.Found {
		t.Fatal("yt-dlp not found after update")
	}
	t.Logf("after: %s (%s) at %s", after.Version, after.Source, after.Path)
	if after.Version == "" {
		t.Error("updated yt-dlp reports no version")
	}
	if before.Version != "" && after.Version < before.Version {
		t.Errorf("version went backwards: %s -> %s", before.Version, after.Version)
	}
}

// TestJSRuntimeDetected checks the runtime yt-dlp needs for YouTube's JS
// challenge is found and formatted the way --js-runtimes expects.
func TestJSRuntimeDetected(t *testing.T) {
	requireE2E(t)
	ctx := context.Background()
	resolver := newResolver()

	tool := resolver.JSRuntime(ctx)
	if !tool.Found {
		t.Skip("no deno/node/bun on this machine")
	}
	spec := resolver.JSRuntimeSpec(ctx)
	t.Logf("js runtime: %s -> %q", tool.Label, spec)

	name, path, ok := strings.Cut(spec, ":")
	if !ok {
		t.Fatalf("spec %q is not RUNTIME:PATH", spec)
	}
	if name != "deno" && name != "node" && name != "bun" {
		t.Errorf("runtime name %q is not one yt-dlp accepts", name)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("spec path does not exist: %v", err)
	}
}

func TestPipelineDownload(t *testing.T) {
	requireE2E(t)
	ctx := context.Background()
	r := &testReporter{t: t}
	resolver := newResolver()

	ytTool := resolver.YtDlp(ctx)
	if !ytTool.Found {
		t.Fatal("yt-dlp not found")
	}
	ffmpegDir := resolver.FFmpegDir(ctx)
	if ffmpegDir == "" {
		t.Fatal("ffmpeg not found")
	}
	t.Logf("yt-dlp %s at %s", ytTool.Version, ytTool.Path)

	info, err := ytdl.FetchInfo(ctx, ytdl.Options{
		YtDlp:     ytTool.Path,
		URL:       testURL(),
		JSRuntime: resolver.JSRuntimeSpec(ctx),
	}, r)
	if err != nil {
		t.Fatalf("FetchInfo: %v", err)
	}
	if info.Title == "" {
		t.Fatal("FetchInfo returned an empty title")
	}
	t.Logf("info: %q by %q, %.0fs", info.Title, info.Uploader, info.Duration)

	outDir := t.TempDir()
	track, err := ytdl.Download(ctx, ytdl.Options{
		YtDlp:     ytTool.Path,
		FfmpegDir: ffmpegDir,
		URL:       testURL(),
		OutDir:    outDir,
		Format:    "wav",
		JSRuntime: resolver.JSRuntimeSpec(ctx),
	}, r)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Ext(track.Path) != ".wav" {
		t.Errorf("expected a .wav file, got %s", track.Path)
	}
	if track.SizeBytes < 100_000 {
		t.Errorf("suspiciously small download: %d bytes", track.SizeBytes)
	}
	t.Logf("downloaded %s (%d bytes)", track.Path, track.SizeBytes)

	// Hand the file to the separation tests via the shared cache below.
	cacheDownload(t, track.Path)
}

// downloadCache keeps one download for the whole test binary so the separation
// tests do not each re-fetch the same video.
var downloadCache string

func cacheDownload(t *testing.T, src string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "demucs-e2e-")
	if err != nil {
		return
	}
	dest := filepath.Join(dir, filepath.Base(src))
	raw, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if err := os.WriteFile(dest, raw, 0o644); err != nil {
		return
	}
	downloadCache = dest
}

// sourceAudio returns a local WAV to separate, downloading one if needed.
func sourceAudio(t *testing.T) string {
	t.Helper()
	if downloadCache != "" {
		return downloadCache
	}
	ctx := context.Background()
	r := &testReporter{t: t}
	resolver := newResolver()
	ytTool := resolver.YtDlp(ctx)
	ffmpegDir := resolver.FFmpegDir(ctx)
	if !ytTool.Found || ffmpegDir == "" {
		t.Skip("yt-dlp/ffmpeg missing")
	}
	track, err := ytdl.Download(ctx, ytdl.Options{
		YtDlp:     ytTool.Path,
		FfmpegDir: ffmpegDir,
		URL:       testURL(),
		OutDir:    t.TempDir(),
		Format:    "wav",
		JSRuntime: resolver.JSRuntimeSpec(ctx),
	}, r)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	cacheDownload(t, track.Path)
	if downloadCache == "" {
		return track.Path
	}
	return downloadCache
}

func TestPipelineSeparateDemucs(t *testing.T) {
	requireE2E(t)
	ctx := context.Background()
	r := &testReporter{t: t}
	resolver := newResolver()

	tool := resolver.Demucs(ctx)
	if !tool.Found {
		t.Skip("demucs not installed")
	}
	t.Logf("demucs %s via %v", tool.Version, tool.Argv)

	backend := demucs.New(func(context.Context) []string { return tool.Argv })
	model := findModel(t, backend.Models(ctx), "demucs:htdemucs_ft")

	if err := backend.EnsureModel(ctx, model, r); err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}
	// After EnsureModel the catalog must report it as installed, which also
	// exercises the sha256-prefix verification.
	if got := findModel(t, backend.Models(ctx), model.ID); !got.Installed {
		t.Fatal("htdemucs_ft still reports as not installed after EnsureModel")
	}

	result, err := backend.Separate(ctx, engine.Request{
		Input:    sourceAudio(t),
		OutDir:   t.TempDir(),
		Model:    model,
		Device:   deviceForTest(ctx, resolver),
		Format:   "wav",
		TwoStems: true,
		Overlap:  0.25,
		Jobs:     2,
	}, r)
	if err != nil {
		t.Fatalf("Separate: %v", err)
	}
	assertStems(t, result, "vocals", "no_vocals")
	t.Logf("demucs produced %d stems in %.1fs", len(result.Stems), result.Seconds)
}

func TestPipelineSeparateRoformer(t *testing.T) {
	requireE2E(t)
	ctx := context.Background()
	r := &testReporter{t: t}
	resolver := newResolver()

	tool := resolver.AudioSeparator(ctx)
	if !tool.Found {
		t.Skip("audio-separator not installed")
	}
	t.Logf("audio-separator %s via %v", tool.Version, tool.Argv)

	backend := roformer.New(func(context.Context) []string { return tool.Argv })
	// Mel-Band Kim FT2 is the cheapest of the recommended RoFormer models.
	model := findModel(t, backend.Models(ctx), "roformer:mel_band_roformer_kim_ft2_unwa.ckpt")

	if err := backend.EnsureModel(ctx, model, r); err != nil {
		t.Fatalf("EnsureModel: %v", err)
	}
	if got := findModel(t, backend.Models(ctx), model.ID); !got.Installed {
		t.Fatal("model still reports as not installed after EnsureModel")
	}

	result, err := backend.Separate(ctx, engine.Request{
		Input:               sourceAudio(t),
		OutDir:              t.TempDir(),
		Model:               model,
		Device:              deviceForTest(ctx, resolver),
		Format:              "wav",
		RoformerSegmentSize: 256,
		RoformerOverlap:     8,
		RoformerBatchSize:   1,
		Normalization:       0.9,
	}, r)
	if err != nil {
		t.Fatalf("Separate: %v", err)
	}
	assertStems(t, result, "vocals")
	t.Logf("roformer produced %d stems in %.1fs", len(result.Stems), result.Seconds)
}

func TestRoformerRefresh(t *testing.T) {
	requireE2E(t)
	ctx := context.Background()
	r := &testReporter{t: t}
	resolver := newResolver()
	tool := resolver.AudioSeparator(ctx)
	if !tool.Found {
		t.Skip("audio-separator not installed")
	}
	backend := roformer.New(func(context.Context) []string { return tool.Argv })
	n, err := backend.Refresh(ctx, r)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n == 0 {
		t.Error("expected to discover extra RoFormer models")
	}
	models := backend.Models(ctx)
	t.Logf("catalog now has %d RoFormer models (%d discovered)", len(models), n)
	for _, m := range models {
		if !strings.Contains(strings.ToLower(m.Name), "roformer") {
			t.Errorf("non-RoFormer model leaked into the catalog: %s", m.Name)
		}
	}
}

func deviceForTest(ctx context.Context, resolver *deps.Resolver) string {
	if g := resolver.DetectGPU(ctx); g.Available {
		return "cuda"
	}
	return "cpu"
}

func findModel(t *testing.T, models []engine.Model, id string) engine.Model {
	t.Helper()
	for _, m := range models {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("model %q not in catalog", id)
	return engine.Model{}
}

func assertStems(t *testing.T, result engine.Result, want ...string) {
	t.Helper()
	if len(result.Stems) == 0 {
		t.Fatal("no stems produced")
	}
	have := map[string]int64{}
	for _, s := range result.Stems {
		have[strings.ToLower(s.Name)] = s.SizeBytes
		if s.SizeBytes < 10_000 {
			t.Errorf("stem %s is only %d bytes", s.Name, s.SizeBytes)
		}
		if _, err := os.Stat(s.Path); err != nil {
			t.Errorf("stem %s missing on disk: %v", s.Name, err)
		}
	}
	for _, w := range want {
		if _, ok := have[w]; !ok {
			t.Errorf("expected a %q stem, got %v", w, keys(have))
		}
	}
	// The first stem must be the vocal one: the UI relies on that ordering.
	if first := strings.ToLower(result.Stems[0].Name); !strings.Contains(first, "vocal") {
		t.Errorf("expected vocals first, got %q", first)
	}
}

func keys(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
