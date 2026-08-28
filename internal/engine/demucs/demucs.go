// Package demucs drives the Demucs CLI (Hybrid Transformer Demucs) against a
// fully local model repository.
//
// Demucs normally downloads its weights from Facebook's CDN into the torch hub
// cache on first use. That is a poor fit for a packaged desktop app: the
// download is invisible, unresumable, and happens inside the separation run. So
// this package manages the weights itself and always invokes demucs with
// `--repo <AppDir>/models/demucs`, which makes every run offline and lets the UI
// show a real download progress bar.
//
// A `--repo` folder is just a flat directory of `<signature>-<sha256prefix>.th`
// checkpoints plus the bag-of-models YAML files that reference them — the same
// layout demucs ships in its own `remote/` folder, which is embedded below.
package demucs

import (
	"context"
	"embed"
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
	"demucs-studio/internal/netfetch"
	"demucs-studio/internal/paths"
	"demucs-studio/internal/proc"
)

// remote holds a copy of demucs' own model index: files.txt maps a signature to
// its download path, and each <bag>.yaml lists the signatures it combines.
//
//go:embed remote
var remote embed.FS

const rootURL = "https://dl.fbaipublicfiles.com/demucs/"

// spec is one catalog entry. bytesPerFile is the real Content-Length of each
// checkpoint, used for an accurate download progress bar.
type spec struct {
	name        string
	label       string
	stems       []string
	note        string
	recommended bool
	bytes       int64
}

var catalog = []spec{
	{
		name:        "htdemucs_ft",
		label:       "htdemucs_ft — Demucs v4 fine-tuned",
		stems:       []string{"vocals", "drums", "bass", "other"},
		note:        "Bag of 4 model, chất lượng cao nhất của Demucs. Chậm hơn ~4x htdemucs.",
		recommended: true,
		bytes:       84141271,
	},
	{
		name:  "htdemucs",
		label: "htdemucs — Demucs v4 (nhanh)",
		stems: []string{"vocals", "drums", "bass", "other"},
		note:  "Model mặc định, 1 lượt chạy. Dùng khi cần nhanh.",
		bytes: 84141911,
	},
	{
		name:  "htdemucs_6s",
		label: "htdemucs_6s — 6 stems",
		stems: []string{"vocals", "drums", "bass", "guitar", "piano", "other"},
		note:  "Tách thêm guitar và piano. Stem piano chất lượng còn hạn chế.",
		bytes: 54996327,
	},
}

// Backend implements engine.Backend for demucs.
type Backend struct {
	// Argv returns the command prefix, e.g. ["demucs"] or
	// ["python", "-m", "demucs.separate"]. Resolved lazily so the UI reflects
	// installs that happen while the app is running.
	Argv func(ctx context.Context) []string

	// verified remembers which checkpoints already passed their SHA-256 check.
	//
	// Without this, Models() — a question the UI asks constantly — hashes every
	// installed checkpoint in full. With htdemucs_ft, htdemucs and htdemucs_6s
	// present that is ~476 MB read and hashed per call, and one "separate" click
	// triggers three such calls (~1.29 GB) plus a blocked first paint, all to
	// re-confirm bytes EnsureModel already verified.
	verifiedMu sync.Mutex
	verified   map[string]stamp
}

// stamp identifies a file version cheaply. Size plus modification time is what
// build systems use for the same purpose: any rewrite of the checkpoint changes
// one of them, so a stale entry cannot survive a replaced file.
type stamp struct {
	size    int64
	modTime time.Time
}

func New(argv func(ctx context.Context) []string) *Backend {
	return &Backend{Argv: argv, verified: map[string]stamp{}}
}

// checkpointOK reports whether a checkpoint is present and matches the SHA-256
// prefix baked into its filename, hashing at most once per file version.
func (b *Backend) checkpointOK(path, checksum string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return false
	}
	current := stamp{size: st.Size(), modTime: st.ModTime()}

	b.verifiedMu.Lock()
	cached, seen := b.verified[path]
	b.verifiedMu.Unlock()
	if seen && cached == current {
		return true
	}

	if netfetch.VerifyFile(path, checksum) != nil {
		return false
	}
	b.verifiedMu.Lock()
	b.verified[path] = current
	b.verifiedMu.Unlock()
	return true
}

// markVerified records a checkpoint that was just written and checked, so the
// next listing does not re-hash it.
func (b *Backend) markVerified(path string) {
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	b.verifiedMu.Lock()
	b.verified[path] = stamp{size: st.Size(), modTime: st.ModTime()}
	b.verifiedMu.Unlock()
}

func (b *Backend) ID() string { return engine.BackendDemucs }

func (b *Backend) Models(ctx context.Context) []engine.Model {
	out := make([]engine.Model, 0, len(catalog))
	for _, s := range catalog {
		files, err := filesFor(s.name)
		installed := err == nil && len(files) > 0
		if installed {
			for _, f := range files {
				if !b.checkpointOK(filepath.Join(paths.DemucsRepoDir(), f.name), f.checksum) {
					installed = false
					break
				}
			}
		}
		out = append(out, engine.Model{
			ID:          engine.BackendDemucs + ":" + s.name,
			Backend:     engine.BackendDemucs,
			Name:        s.name,
			Label:       s.label,
			Family:      "Demucs v4 (Hybrid Transformer)",
			Stems:       s.stems,
			Note:        s.note,
			SizeBytes:   int64(len(files)) * s.bytes,
			Installed:   installed,
			Recommended: s.recommended,
			SupportsGPU: true,
			// demucs takes --two-stems.
			TwoStemsOption: true,
		})
	}
	return out
}

// EnsureModel makes every checkpoint of a bag available in the local repo,
// importing from the torch hub cache when possible and downloading otherwise.
func (b *Backend) EnsureModel(ctx context.Context, m engine.Model, r engine.Reporter) error {
	repo := paths.DemucsRepoDir()
	if err := paths.EnsureDir(repo); err != nil {
		return err
	}
	// The YAML must sit next to the weights: demucs resolves the bag name by
	// scanning the repo folder for `<name>.yaml`.
	if err := writeYAML(repo, m.Name); err != nil {
		return err
	}

	files, err := filesFor(m.Name)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, f := range files {
		total += f.size
	}
	var doneBefore int64

	for i, f := range files {
		dest := filepath.Join(repo, f.name)
		if b.checkpointOK(dest, f.checksum) {
			r.Logf("info", "Đã có %s", f.name)
			doneBefore += f.size
			continue
		}
		// A machine that already ran demucs has the exact same file cached.
		// The source is hashed directly: it is read once, and it is not ours to
		// keep stamps for.
		if src := filepath.Join(paths.TorchHubCache(), f.name); localOK(src, f.checksum) {
			r.Logf("info", "Lấy %s từ cache torch", f.name)
			r.Step("model", float64(doneBefore)/float64(total), "Nhập model từ cache", f.name)
			if err := copyFile(src, dest); err != nil {
				return err
			}
			// The copy is byte-for-byte from a file we just verified.
			b.markVerified(dest)
			doneBefore += f.size
			continue
		}

		url := f.url
		label := fmt.Sprintf("Tải model %s (%d/%d)", m.Name, i+1, len(files))
		r.Logf("info", "Tải %s", url)
		base := doneBefore
		err := netfetch.Get(ctx, url, dest, f.checksum, func(done, _ int64) {
			r.Step("model", float64(base+done)/float64(total), label,
				fmt.Sprintf("%s — %s", f.name, byteRange(base+done, total)))
		})
		if err != nil {
			return fmt.Errorf("tải %s thất bại: %w", f.name, err)
		}
		// netfetch.Get already verified the digest during the download.
		b.markVerified(dest)
		doneBefore += f.size
	}
	r.Step("model", 1, "Model đã sẵn sàng", m.Name)
	return nil
}

// Separate runs demucs and reports progress derived from its tqdm bars.
func (b *Backend) Separate(ctx context.Context, req engine.Request, r engine.Reporter) (engine.Result, error) {
	argv := b.Argv(ctx)
	if len(argv) == 0 {
		return engine.Result{}, errors.New("chưa tìm thấy Demucs. Hãy cài ở tab Phụ thuộc")
	}
	// demucs writes into <out>/<model name>; clear that so a previous run's
	// stems cannot be reported as this one's.
	dir := filepath.Join(req.OutDir, req.Model.Name)
	if err := engine.ResetOutputDir(dir); err != nil {
		return engine.Result{}, err
	}

	args := append([]string{}, argv[1:]...)
	args = append(args,
		"-n", req.Model.Name,
		"--repo", paths.DemucsRepoDir(),
		"-o", req.OutDir,
		"--filename", "{stem}.{ext}",
		"--overlap", fmt.Sprintf("%.2f", req.Overlap),
		"--shifts", fmt.Sprint(req.Shifts),
	)
	if req.Device == "cuda" || req.Device == "cpu" {
		args = append(args, "-d", req.Device)
	}
	if req.Segment > 0 {
		args = append(args, "--segment", fmt.Sprint(req.Segment))
	}
	// --jobs spawns worker processes and only helps on CPU.
	if req.Jobs > 1 && req.Device != "cuda" {
		args = append(args, "-j", fmt.Sprint(req.Jobs))
	}
	if req.TwoStems {
		args = append(args, "--two-stems", "vocals")
	}
	switch req.Format {
	case "mp3":
		args = append(args, "--mp3", "--mp3-bitrate", fmt.Sprint(req.Mp3Rate))
	case "flac":
		args = append(args, "--flac")
	}
	args = append(args, req.Input)

	r.Logf("info", "%s %s", filepath.Base(argv[0]), strings.Join(args, " "))

	// A bag of N models with S shifts draws N*max(S,1) separate tqdm bars, each
	// running 0->100%. Track which bar we are on to build one overall figure.
	expectedBars := len(mustFiles(req.Model.Name))
	if req.Shifts > 0 {
		expectedBars *= req.Shifts
	}
	if expectedBars < 1 {
		expectedBars = 1
	}
	barIndex := 0
	lastFraction := 0.0
	started := time.Now()

	err := proc.Run(ctx, proc.Options{
		Bin:         argv[0],
		Args:        args,
		ExtraEnv:    separationEnv(),
		PrependPath: []string{filepath.Dir(argv[0])},
		OnLine: func(stream, line string) {
			if f, ok := engine.ParseTqdm(line); ok {
				// A drop back towards zero means the next sub-model started.
				if f+0.05 < lastFraction {
					barIndex++
					if barIndex >= expectedBars {
						barIndex = expectedBars - 1
					}
				}
				lastFraction = f
				overall := (float64(barIndex) + f) / float64(expectedBars)
				detail := engine.TqdmTail(line)
				if expectedBars > 1 {
					detail = fmt.Sprintf("model %d/%d  %s", barIndex+1, expectedBars, detail)
				}
				r.Step("separate", overall, "Đang tách bằng "+req.Model.Name, strings.TrimSpace(detail))
				return
			}
			level := "info"
			if stream == "stderr" && looksLikeError(line) {
				level = "warn"
			}
			r.Log(level, line)
		},
	})
	if err != nil {
		return engine.Result{}, err
	}

	stems, err := collectStems(dir)
	if err != nil {
		return engine.Result{}, err
	}
	if len(stems) == 0 {
		return engine.Result{}, fmt.Errorf("demucs chạy xong nhưng không thấy stem nào trong %s", dir)
	}
	r.Step("separate", 1, "Tách xong", req.Model.Name)
	return engine.Result{
		ModelID:   req.Model.ID,
		ModelName: req.Model.Name,
		Dir:       dir,
		Stems:     stems,
		Seconds:   time.Since(started).Seconds(),
	}, nil
}

// separationEnv builds the environment for a demucs run.
//
// TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD deserves an explanation. PyTorch 2.6 flipped
// `torch.load`'s `weights_only` default to True, and demucs 4.0.1 still ships
// checkpoints that pickle the whole HTDemucs object rather than a bare state
// dict — so on any modern torch, `demucs -n htdemucs_ft` dies with
// "UnpicklingError: Unsupported global: GLOBAL demucs.htdemucs.HTDemucs".
//
// Loading a pickle means executing whatever it contains, so this is only safe
// because EnsureModel has already verified every checkpoint against the SHA-256
// prefix baked into its filename by Meta. We never load a file we did not
// checksum.
func separationEnv() []string {
	return []string{
		"PYTHONUNBUFFERED=1",
		"PYTHONIOENCODING=utf-8",
		"TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1",
	}
}

// --- model index -------------------------------------------------------------

type modelFile struct {
	name     string // "f7e0c4bc-ba3fe64a.th"
	checksum string // "ba3fe64a" — sha256 prefix, exactly what demucs verifies
	url      string
	size     int64
}

// bagModels extracts the signature list from a bag YAML. The embedded files are
// a fixed, single-line-per-key format ("models: ['a', 'b']"), so a targeted
// regex is more honest here than pulling in a YAML dependency.
var (
	bagModelsRe = regexp.MustCompile(`(?m)^models:\s*\[([^\]]*)\]`)
	quotedRe    = regexp.MustCompile(`'([^']+)'|"([^"]+)"`)
)

func bagModels(yaml string) []string {
	m := bagModelsRe.FindStringSubmatch(yaml)
	if m == nil {
		return nil
	}
	var out []string
	for _, q := range quotedRe.FindAllStringSubmatch(m[1], -1) {
		if q[1] != "" {
			out = append(out, q[1])
		} else if q[2] != "" {
			out = append(out, q[2])
		}
	}
	return out
}

// remoteIndex parses files.txt into signature -> download URL.
func remoteIndex() (map[string]string, error) {
	raw, err := remote.ReadFile("remote/files.txt")
	if err != nil {
		return nil, err
	}
	index := map[string]string{}
	root := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "root:"); ok {
			root = strings.TrimSpace(rest)
			continue
		}
		sig, _, _ := strings.Cut(line, "-")
		index[sig] = rootURL + root + line
	}
	return index, nil
}

func filesFor(model string) ([]modelFile, error) {
	yaml, err := remote.ReadFile("remote/" + model + ".yaml")
	if err != nil {
		return nil, fmt.Errorf("không biết model %q", model)
	}
	sigs := bagModels(string(yaml))
	if len(sigs) == 0 {
		return nil, fmt.Errorf("bag %q không khai báo model nào", model)
	}
	index, err := remoteIndex()
	if err != nil {
		return nil, err
	}
	var size int64
	for _, s := range catalog {
		if s.name == model {
			size = s.bytes
		}
	}

	out := make([]modelFile, 0, len(sigs))
	for _, sig := range sigs {
		url, ok := index[sig]
		if !ok {
			return nil, fmt.Errorf("không có URL cho signature %s", sig)
		}
		name := url[strings.LastIndex(url, "/")+1:]
		checksum := ""
		if _, sum, ok := strings.Cut(strings.TrimSuffix(name, ".th"), "-"); ok {
			checksum = sum
		}
		out = append(out, modelFile{name: name, checksum: checksum, url: url, size: size})
	}
	return out, nil
}

func mustFiles(model string) []modelFile {
	f, err := filesFor(model)
	if err != nil {
		return []modelFile{{}}
	}
	return f
}

// writeYAML copies the bag definition into the local repo folder.
func writeYAML(repo, model string) error {
	raw, err := remote.ReadFile("remote/" + model + ".yaml")
	if err != nil {
		// A bare signature is not a bag and needs no YAML.
		return nil
	}
	return os.WriteFile(filepath.Join(repo, model+".yaml"), raw, 0o644)
}

// localOK reports whether a checkpoint is present and passes the sha256-prefix
// check demucs itself performs.
func localOK(path, checksum string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() == 0 {
		return false
	}
	return netfetch.VerifyFile(path, checksum) == nil
}

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
		stems = append(stems, engine.Stem{
			Name:      strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			Path:      filepath.Join(dir, e.Name()),
			SizeBytes: info.Size(),
		})
	}
	// Vocals first — it is what the user came for.
	sort.SliceStable(stems, func(i, j int) bool {
		return rank(stems[i].Name) < rank(stems[j].Name)
	})
	return stems, nil
}

func rank(name string) int {
	switch strings.ToLower(name) {
	case "vocals":
		return 0
	case "no_vocals", "instrumental":
		return 1
	default:
		return 2
	}
}

func copyFile(src, dest string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, raw, 0o644)
}

func looksLikeError(line string) bool {
	l := strings.ToLower(line)
	for _, needle := range []string{"error", "traceback", "warning", "cannot", "failed"} {
		if strings.Contains(l, needle) {
			return true
		}
	}
	return false
}

func byteRange(done, total int64) string {
	return humanBytes(done) + " / " + humanBytes(total)
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
