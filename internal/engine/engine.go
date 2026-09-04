// Package engine defines the separation-backend abstraction. Two backends
// implement it: demucs (Hybrid Transformer Demucs, notably htdemucs_ft) and
// roformer (BS-RoFormer / Mel-Band RoFormer via audio-separator).
package engine

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Backend identifiers, used as the prefix of a fully-qualified model id
// ("demucs:htdemucs_ft", "roformer:bs_roformer_vocals_resurrection_unwa").
const (
	BackendDemucs   = "demucs"
	BackendRoformer = "roformer"
)

// Model is one selectable separation model as presented in the UI.
type Model struct {
	ID          string   `json:"id"` // "<backend>:<name>"
	Backend     string   `json:"backend"`
	Name        string   `json:"name"`   // backend-native name / checkpoint file
	Label       string   `json:"label"`  // human-readable
	Family      string   `json:"family"` // grouping in the dropdown
	Stems       []string `json:"stems"`
	Note        string   `json:"note"`
	SizeBytes   int64    `json:"sizeBytes"`
	Installed   bool     `json:"installed"`
	Recommended bool     `json:"recommended"`
	SupportsGPU bool     `json:"supportsGpu"`
	// TwoStemsOption reports whether this model honours Request.TwoStems. The
	// UI must not offer a stem choice that the selected backend ignores.
	TwoStemsOption bool `json:"twoStemsOption"`
}

// Stem is one produced audio file.
type Stem struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"sizeBytes"`
}

// Request is a separation job.
type Request struct {
	Input  string // absolute path to the source audio
	OutDir string // job directory; the backend may create subfolders
	Model  Model
	// Device is already resolved when it reaches a backend: the app turns
	// "auto" into the accelerator it verified. "mps" is Apple Silicon's Metal
	// backend.
	Device  string // cuda | mps | cpu
	Format  string // wav | flac | mp3
	Mp3Rate int

	// Demucs knobs.
	TwoStems bool
	Shifts   int
	Overlap  float64
	Segment  int
	Jobs     int

	// Roformer knobs.
	RoformerSegmentSize int
	RoformerOverlap     int
	RoformerBatchSize   int
	Normalization       float64
}

// Result is the outcome of a separation.
type Result struct {
	ModelID   string  `json:"modelId"`
	ModelName string  `json:"modelName"`
	Dir       string  `json:"dir"`
	Stems     []Stem  `json:"stems"`
	Seconds   float64 `json:"seconds"`
}

// Reporter is how a backend reports back while working.
type Reporter interface {
	Log(level, text string)
	Logf(level, format string, args ...any)
	// Step reports fractional completion in [0,1]; pass -1 when unknown.
	Step(phase string, fraction float64, label, detail string)
}

// Backend is a separation implementation.
type Backend interface {
	ID() string
	// Models lists this backend's models with Installed reflecting local disk.
	Models(ctx context.Context) []Model
	// EnsureModel makes the model's weights available locally.
	EnsureModel(ctx context.Context, m Model, r Reporter) error
	// Separate runs the model over req.Input.
	Separate(ctx context.Context, req Request, r Reporter) (Result, error)
}

// ResetOutputDir empties a model's output directory before a run.
//
// Both backends discover their results by listing the directory afterwards, and
// the directory name is derived from the input filename, so it is the same on
// every run of the same track. Without this, switching format or stem count and
// re-running leaves the previous run's files behind and they get reported as
// part of the new result — wrong stems, wrong format, wrong sizes.
//
// Only the per-model subdirectory is removed, so results from a *different*
// model on the same track survive and stay comparable.
func ResetOutputDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("không xoá được kết quả cũ ở %s: %w", dir, err)
	}
	return os.MkdirAll(dir, 0o755)
}

// SplitID splits "demucs:htdemucs_ft" into its backend and model name.
func SplitID(id string) (backend, name string) {
	b, n, ok := strings.Cut(id, ":")
	if !ok {
		// Unprefixed ids are legacy demucs model names.
		return BackendDemucs, id
	}
	return b, n
}

// tqdmPercent matches the " 34%|███" prefix of a tqdm bar as well as the
// "34.0%" form some tools print.
var tqdmPercent = regexp.MustCompile(`(?:^|[^\d.])(\d{1,3})(?:\.(\d+))?%`)

// ParseTqdm extracts a 0..1 fraction from a tqdm-style progress line.
// ok is false when the line carries no percentage.
//
// A bare percentage is not enough to accept a line: demucs and
// audio-separator both emit plenty of prose, and a log message that happens to
// mention "50%" would otherwise yank the progress bar backwards. Every real
// tqdm line carries the bar separator "|" or the rate/ETA bracket "[…]", so
// require one of those too.
func ParseTqdm(line string) (fraction float64, ok bool) {
	if !strings.ContainsAny(line, "|[") {
		return 0, false
	}
	m := tqdmPercent.FindStringSubmatch(line)
	if m == nil {
		return 0, false
	}
	whole, err := strconv.Atoi(m[1])
	if err != nil || whole > 100 {
		return 0, false
	}
	value := float64(whole)
	if m[2] != "" {
		if frac, err := strconv.ParseFloat("0."+m[2], 64); err == nil {
			value += frac
		}
	}
	return value / 100, true
}

// TqdmTail returns the rate/ETA part of a tqdm line (what follows the bar), for
// display next to the progress bar.
func TqdmTail(line string) string {
	if i := strings.LastIndex(line, "["); i >= 0 {
		if j := strings.LastIndex(line, "]"); j > i {
			return line[i+1 : j]
		}
	}
	return ""
}
