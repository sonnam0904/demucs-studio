// Package roformer drives BS-RoFormer and Mel-Band RoFormer separation through
// the `audio-separator` CLI.
//
// Unlike Demucs, the RoFormer checkpoints have no single official distribution
// with a stable index we can mirror; audio-separator owns that mapping and its
// downloader. So this backend delegates fetching to `audio-separator
// --download_model_only --model_file_dir <local dir>`, which keeps everything
// under the app's own models folder while letting the tool resolve URLs.
//
// The model list is a curated set of the strongest vocal/instrumental
// checkpoints, and can be refreshed at runtime from `--list_models` so newly
// published models show up without an app update.
package roformer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"demucs-studio/internal/engine"
	"demucs-studio/internal/paths"
	"demucs-studio/internal/proc"
)

// entry is a curated catalog record. checkpoint is the filename
// audio-separator uses as its model identifier.
type entry struct {
	checkpoint  string
	label       string
	family      string
	stems       []string
	note        string
	recommended bool
}

// curated lists models worth surfacing by default, best-first within a family.
var curated = []entry{
	{
		checkpoint:  "bs_roformer_vocals_resurrection_unwa.ckpt",
		label:       "BS-RoFormer — Vocals Resurrection (unwa)",
		family:      "BS-RoFormer",
		stems:       []string{"vocals", "instrumental"},
		note:        "BS-RoFormer vocal mạnh nhất hiện tại. Nặng hơn Demucs, nên dùng GPU.",
		recommended: true,
	},
	{
		checkpoint: "bs_roformer_vocals_revive_v3e_unwa.ckpt",
		label:      "BS-RoFormer — Vocals Revive V3e (unwa)",
		family:     "BS-RoFormer",
		stems:      []string{"vocals", "instrumental"},
		note:       "Giữ được nhiều chi tiết vocal, ít bleed.",
	},
	{
		checkpoint: "BS-Roformer-SW.ckpt",
		label:      "BS-RoFormer — SW (jarredou)",
		family:     "BS-RoFormer",
		stems:      []string{"vocals", "instrumental"},
		note:       "Bản fine-tune cân bằng giữa vocal và nhạc nền.",
	},
	{
		checkpoint: "bs_roformer_vocals_gabox.ckpt",
		label:      "BS-RoFormer — Vocals (Gabox)",
		family:     "BS-RoFormer",
		stems:      []string{"vocals", "instrumental"},
		note:       "Nhẹ hơn, phù hợp máy cấu hình vừa.",
	},
	{
		checkpoint: "bs_roformer_instrumental_resurrection_unwa.ckpt",
		label:      "BS-RoFormer — Instrumental Resurrection (unwa)",
		family:     "BS-RoFormer",
		stems:      []string{"instrumental", "vocals"},
		note:       "Tối ưu cho beat/karaoke thay vì vocal.",
	},
	{
		checkpoint: "bs_roformer_karaoke_anvuew.ckpt",
		label:      "BS-RoFormer — Karaoke (anvuew)",
		family:     "BS-RoFormer",
		stems:      []string{"lead vocals", "backing vocals"},
		note:       "Tách vocal chính khỏi hát đệm (chạy sau khi đã có stem vocals).",
	},
	{
		checkpoint:  "mel_band_roformer_kim_ft2_unwa.ckpt",
		label:       "Mel-Band RoFormer — Kim FT2 (unwa)",
		family:      "Mel-Band RoFormer",
		stems:       []string{"vocals", "instrumental"},
		note:        "Rất phổ biến, chất lượng vocal xuất sắc và nhanh hơn BS-RoFormer.",
		recommended: true,
	},
	{
		checkpoint: "melband_roformer_big_beta6x.ckpt",
		label:      "Mel-Band RoFormer — Big Beta 6X (unwa)",
		family:     "Mel-Band RoFormer",
		stems:      []string{"vocals", "instrumental"},
		note:       "Model lớn, vocal đầy đặn.",
	},
	{
		checkpoint: "vocals_mel_band_roformer.ckpt",
		label:      "Mel-Band RoFormer — Vocals (Kimberley Jensen)",
		family:     "Mel-Band RoFormer",
		stems:      []string{"vocals", "instrumental"},
		note:       "Bản gốc, ổn định, làm mốc so sánh tốt.",
	},
	{
		checkpoint: "mel_band_roformer_karaoke_aufr33_viperx_sdr_10.1956.ckpt",
		label:      "Mel-Band RoFormer — Karaoke (aufr33/viperx)",
		family:     "Mel-Band RoFormer",
		stems:      []string{"lead vocals", "backing vocals"},
		note:       "Tách vocal chính / hát đệm.",
	},
}

// Backend implements engine.Backend for the RoFormer family.
type Backend struct {
	// Argv resolves the audio-separator command prefix lazily.
	Argv func(ctx context.Context) []string

	mu    sync.Mutex
	extra []entry // models discovered via --list_models
}

func New(argv func(ctx context.Context) []string) *Backend { return &Backend{Argv: argv} }

func (b *Backend) ID() string { return engine.BackendRoformer }

func (b *Backend) Models(ctx context.Context) []engine.Model {
	b.mu.Lock()
	all := append(append([]entry(nil), curated...), b.extra...)
	b.mu.Unlock()

	dir := paths.RoformerModelDir()
	out := make([]engine.Model, 0, len(all))
	for _, e := range all {
		size := int64(0)
		installed := false
		if st, err := os.Stat(filepath.Join(dir, e.checkpoint)); err == nil && st.Size() > 0 {
			installed, size = true, st.Size()
		}
		out = append(out, engine.Model{
			ID:          engine.BackendRoformer + ":" + e.checkpoint,
			Backend:     engine.BackendRoformer,
			Name:        e.checkpoint,
			Label:       e.label,
			Family:      e.family,
			Stems:       e.stems,
			Note:        e.note,
			SizeBytes:   size,
			Installed:   installed,
			Recommended: e.recommended,
			SupportsGPU: true,
			// audio-separator always emits the model's own two stems; there is
			// no equivalent of --two-stems, so the UI must not pretend there is.
			TwoStemsOption: false,
		})
	}
	return out
}

// Refresh pulls the live model list from audio-separator and merges in any
// RoFormer checkpoints the curated list does not already cover.
func (b *Backend) Refresh(ctx context.Context, r engine.Reporter) (int, error) {
	argv := b.Argv(ctx)
	if len(argv) == 0 {
		return 0, errors.New("chưa tìm thấy audio-separator")
	}
	// --list_filter is only honoured by the "pretty" formatter, so ask for the
	// full JSON dump and filter here.
	args := append([]string{}, argv[1:]...)
	args = append(args, "--list_models", "--list_format", "json")

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	out, err := proc.Output(ctx, proc.Options{
		Bin:      argv[0],
		Args:     args,
		ExtraEnv: []string{"PYTHONUNBUFFERED=1"},
		OnLine: func(stream, line string) {
			if stream == "stderr" {
				r.Log("debug", line)
			}
		},
	})
	if err != nil {
		return 0, err
	}

	discovered := parseModelList(out)
	known := map[string]bool{}
	for _, e := range curated {
		known[e.checkpoint] = true
	}

	var added []entry
	for _, d := range discovered {
		if known[d.checkpoint] {
			continue
		}
		known[d.checkpoint] = true
		added = append(added, d)
	}
	sort.Slice(added, func(i, j int) bool { return added[i].checkpoint < added[j].checkpoint })

	b.mu.Lock()
	b.extra = added
	b.mu.Unlock()
	r.Logf("info", "Tìm thấy thêm %d model RoFormer", len(added))
	return len(added), nil
}

// EnsureModel downloads the checkpoint (and its config YAML) into the app's
// model folder without running a separation.
func (b *Backend) EnsureModel(ctx context.Context, m engine.Model, r engine.Reporter) error {
	if !isBareFilename(m.Name) {
		return fmt.Errorf("tên checkpoint không hợp lệ: %q", m.Name)
	}
	dir := paths.RoformerModelDir()
	if err := paths.EnsureDir(dir); err != nil {
		return err
	}
	if st, err := os.Stat(filepath.Join(dir, m.Name)); err == nil && st.Size() > 0 {
		r.Logf("info", "Model đã có sẵn: %s", m.Name)
		return nil
	}
	argv := b.Argv(ctx)
	if len(argv) == 0 {
		return errors.New("chưa tìm thấy audio-separator. Hãy cài ở tab Phụ thuộc")
	}

	args := append([]string{}, argv[1:]...)
	args = append(args,
		"--download_model_only",
		"-m", m.Name,
		"--model_file_dir", dir,
		"--log_level", "info",
	)
	r.Step("model", -1, "Đang tải model "+m.Label, m.Name)
	r.Logf("info", "%s %s", filepath.Base(argv[0]), strings.Join(args, " "))

	err := proc.Run(ctx, proc.Options{
		Bin:      argv[0],
		Args:     args,
		ExtraEnv: []string{"PYTHONUNBUFFERED=1"},
		OnLine: func(stream, line string) {
			if f, ok := engine.ParseTqdm(line); ok {
				r.Step("model", f, "Đang tải model "+m.Label, engine.TqdmTail(line))
				return
			}
			r.Log("info", line)
		},
	})
	if err != nil {
		return err
	}
	if st, statErr := os.Stat(filepath.Join(dir, m.Name)); statErr != nil || st.Size() == 0 {
		return fmt.Errorf("tải xong nhưng không thấy %s trong %s", m.Name, dir)
	}
	r.Step("model", 1, "Model đã sẵn sàng", m.Name)
	return nil
}

func (b *Backend) Separate(ctx context.Context, req engine.Request, r engine.Reporter) (engine.Result, error) {
	argv := b.Argv(ctx)
	if len(argv) == 0 {
		return engine.Result{}, errors.New("chưa tìm thấy audio-separator. Hãy cài ở tab Phụ thuộc")
	}
	if !isBareFilename(req.Model.Name) {
		return engine.Result{}, fmt.Errorf("tên checkpoint không hợp lệ: %q", req.Model.Name)
	}
	// Mirror demucs' layout: results go into <job>/<model>/.
	outDir := filepath.Join(req.OutDir, strings.TrimSuffix(req.Model.Name, filepath.Ext(req.Model.Name)))
	// Clear it first: results are discovered by listing this directory, so
	// leftovers from an earlier run of the same model would be reported as new.
	if err := engine.ResetOutputDir(outDir); err != nil {
		return engine.Result{}, err
	}

	args := append([]string{}, argv[1:]...)
	args = append(args,
		"-m", req.Model.Name,
		"--model_file_dir", paths.RoformerModelDir(),
		"--output_dir", outDir,
		"--output_format", strings.ToUpper(req.Format),
		"--normalization", fmt.Sprintf("%.2f", req.Normalization),
		"--mdxc_segment_size", fmt.Sprint(req.RoformerSegmentSize),
		"--mdxc_overlap", fmt.Sprint(req.RoformerOverlap),
		"--mdxc_batch_size", fmt.Sprint(req.RoformerBatchSize),
		"--log_level", "info",
	)
	if req.Format == "mp3" && req.Mp3Rate > 0 {
		args = append(args, "--output_bitrate", fmt.Sprintf("%dk", req.Mp3Rate))
	}

	env := []string{"PYTHONUNBUFFERED=1", "PYTHONIOENCODING=utf-8"}
	if req.Device == "cpu" {
		// audio-separator has no device flag; hiding the GPU is how you force CPU.
		env = append(env, "CUDA_VISIBLE_DEVICES=")
	} else {
		// Mixed precision roughly halves RoFormer inference time on CUDA and is
		// meaningless on CPU.
		args = append(args, "--use_autocast")
	}
	args = append(args, req.Input)

	r.Logf("info", "%s %s", filepath.Base(argv[0]), strings.Join(args, " "))
	r.Step("separate", -1, "Đang nạp model "+req.Model.Label, "")
	started := time.Now()

	err := proc.Run(ctx, proc.Options{
		Bin:      argv[0],
		Args:     args,
		ExtraEnv: env,
		OnLine: func(stream, line string) {
			if f, ok := engine.ParseTqdm(line); ok {
				r.Step("separate", f, "Đang tách bằng "+req.Model.Label, engine.TqdmTail(line))
				return
			}
			level := "info"
			if stream == "stderr" && strings.Contains(strings.ToLower(line), "error") {
				level = "warn"
			}
			r.Log(level, line)
		},
	})
	if err != nil {
		return engine.Result{}, err
	}

	stems, err := collectStems(outDir)
	if err != nil {
		return engine.Result{}, err
	}
	if len(stems) == 0 {
		return engine.Result{}, fmt.Errorf("audio-separator chạy xong nhưng không thấy stem nào trong %s", outDir)
	}
	r.Step("separate", 1, "Tách xong", req.Model.Label)
	return engine.Result{
		ModelID:   req.Model.ID,
		ModelName: req.Model.Name,
		Dir:       outDir,
		Stems:     stems,
		Seconds:   time.Since(started).Seconds(),
	}, nil
}

// parseModelList extracts the RoFormer entries from `--list_models
// --list_format json`. The payload is {architecture: {label: {filename, stems,
// scores}}}; RoFormer models all live under the MDXC architecture, but we key
// off the filename so a re-classification upstream does not hide them.
func parseModelList(out string) []entry {
	// The tool logs to stderr, but be defensive about stray leading output.
	start := strings.Index(out, "{")
	if start < 0 {
		return fallbackScan(out)
	}
	var doc map[string]map[string]struct {
		Filename string   `json:"filename"`
		Stems    []string `json:"stems"`
	}
	if err := json.Unmarshal([]byte(out[start:]), &doc); err != nil {
		return fallbackScan(out)
	}

	var found []entry
	for _, models := range doc {
		for label, meta := range models {
			name := meta.Filename
			if name == "" || !strings.Contains(strings.ToLower(name), "roformer") {
				continue
			}
			if !isBareFilename(name) {
				continue
			}
			stems := meta.Stems
			if len(stems) == 0 {
				stems = []string{"vocals", "instrumental"}
			}
			found = append(found, entry{
				checkpoint: name,
				label:      cleanLabel(label),
				family:     familyOf(name) + " · thêm",
				stems:      stems,
				note:       "Phát hiện từ danh sách của audio-separator.",
			})
		}
	}
	return found
}

// fallbackScan is used when the JSON shape changes: scrape checkpoint names out
// of the raw output so the feature degrades instead of breaking.
var ckptRe = regexp.MustCompile(`[A-Za-z0-9._\-]*[Rr]oformer[A-Za-z0-9._\-]*\.ckpt`)

func fallbackScan(out string) []entry {
	seen := map[string]bool{}
	var found []entry
	for _, name := range ckptRe.FindAllString(out, -1) {
		if seen[name] || !isBareFilename(name) {
			continue
		}
		seen[name] = true
		found = append(found, entry{
			checkpoint: name,
			label:      prettify(name),
			family:     familyOf(name) + " · thêm",
			stems:      []string{"vocals", "instrumental"},
			note:       "Phát hiện từ danh sách của audio-separator.",
		})
	}
	return found
}

// isBareFilename rejects anything that is not a plain filename.
//
// The checkpoint name arrives from audio-separator's model index, which is
// fetched over the network at runtime, and it is then joined onto both the model
// directory and the job output directory. A name like
// "../../../.config/autostart/x.ckpt" would pass the RoFormer filter, be
// published as a selectable model, and be accepted by the caller's id lookup
// precisely *because* it is in the list — writing the checkpoint and every stem
// outside the app's tree.
func isBareFilename(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		return false
	}
	if strings.HasPrefix(name, ".") {
		return false
	}
	// filepath.Base collapses any separator form the current OS understands.
	return filepath.Base(name) == name
}

// cleanLabel strips the redundant "Roformer Model: " prefix audio-separator
// puts on its display names.
func cleanLabel(label string) string {
	for _, prefix := range []string{"Roformer Model: ", "MDXC Model: ", "MDX23C Model: "} {
		label = strings.TrimPrefix(label, prefix)
	}
	return strings.TrimSpace(label)
}

// audio-separator names outputs "<input>_(Vocals)_<model>.wav"; pull the stem
// label back out of the parentheses.
var stemNameRe = regexp.MustCompile(`\(([^)]+)\)`)

func collectStems(dir string) ([]engine.Stem, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var stems []engine.Stem
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".wav" && ext != ".mp3" && ext != ".flac" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		if m := stemNameRe.FindStringSubmatch(e.Name()); m != nil {
			name = strings.ToLower(m[1])
		}
		stems = append(stems, engine.Stem{
			Name:      name,
			Path:      filepath.Join(dir, e.Name()),
			SizeBytes: info.Size(),
		})
	}
	sort.SliceStable(stems, func(i, j int) bool { return rank(stems[i].Name) < rank(stems[j].Name) })
	return stems, nil
}

func rank(name string) int {
	switch strings.ToLower(name) {
	case "vocals", "lead vocals":
		return 0
	case "instrumental", "no_vocals":
		return 1
	default:
		return 2
	}
}

func familyOf(checkpoint string) string {
	l := strings.ToLower(checkpoint)
	switch {
	case strings.Contains(l, "mel_band") || strings.Contains(l, "melband"):
		return "Mel-Band RoFormer"
	case strings.Contains(l, "roformer"):
		return "BS-RoFormer"
	default:
		return "RoFormer"
	}
}

// prettify turns a checkpoint filename into something readable in a dropdown.
func prettify(checkpoint string) string {
	s := strings.TrimSuffix(checkpoint, filepath.Ext(checkpoint))
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
