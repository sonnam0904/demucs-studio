// Package settings persists user preferences as JSON in the app data dir.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"demucs-studio/internal/paths"
)

// Settings is the full persisted configuration. Every field is also the shape
// the frontend sees, so json tags are lowerCamelCase to match TypeScript.
type Settings struct {
	OutputDir string `json:"outputDir"`

	// Explicit executable overrides. Empty means "auto-detect".
	YtDlpPath          string `json:"ytDlpPath"`
	FfmpegPath         string `json:"ffmpegPath"`
	PythonPath         string `json:"pythonPath"`
	DemucsPath         string `json:"demucsPath"`
	AudioSeparatorPath string `json:"audioSeparatorPath"`

	// Engine install choices, remembered so reopening the app does not silently
	// reset them. Accel empty means "never chosen", which is what lets the UI
	// fall back to App.SuggestedAccel for a first-time user without overriding
	// a deliberate choice afterwards.
	Accel   string `json:"accel"`   // "", reuse | cuda | mps | cpu
	CudaTag string `json:"cudaTag"` // cu118 | cu121 | cu124 | cu126 | cu128 | cu130

	// Download options.
	AudioFormat        string `json:"audioFormat"`        // wav | flac | mp3
	CookiesFromBrowser string `json:"cookiesFromBrowser"` // "", chrome, firefox, edge, ...

	// Separation options.
	ModelID    string  `json:"modelId"`
	Device     string  `json:"device"` // auto | cuda | cpu
	TwoStems   bool    `json:"twoStems"`
	Shifts     int     `json:"shifts"`
	Overlap    float64 `json:"overlap"`
	Segment    int     `json:"segment"` // 0 = model default
	Jobs       int     `json:"jobs"`
	StemFormat string  `json:"stemFormat"` // wav | flac | mp3
	Mp3Bitrate int     `json:"mp3Bitrate"`

	// Roformer-specific knobs (audio-separator MDXC architecture).
	RoformerSegmentSize int     `json:"roformerSegmentSize"`
	RoformerOverlap     int     `json:"roformerOverlap"`
	RoformerBatchSize   int     `json:"roformerBatchSize"`
	Normalization       float64 `json:"normalization"`
}

func Defaults() Settings {
	jobs := runtime.NumCPU() / 2
	if jobs < 1 {
		jobs = 1
	}
	if jobs > 4 {
		jobs = 4
	}
	return Settings{
		OutputDir: paths.DefaultOutputDir(),
		// Accel is deliberately left empty: no default beats asking
		// SuggestedAccel what this particular machine should use.
		CudaTag:             "cu126",
		AudioFormat:         "wav",
		ModelID:             "demucs:htdemucs_ft",
		Device:              "auto",
		TwoStems:            true,
		Shifts:              0,
		Overlap:             0.25,
		Segment:             0,
		Jobs:                jobs,
		StemFormat:          "wav",
		Mp3Bitrate:          320,
		RoformerSegmentSize: 256,
		RoformerOverlap:     8,
		RoformerBatchSize:   1,
		Normalization:       0.9,
	}
}

// AccelApplies reports whether a PyTorch flavour can be installed on the named
// platform at all. This is the single definition of that rule.
//
// Not a taste question, and not something the UI can be trusted to enforce: the
// settings file lives in the data directory and travels with it, so a value can
// arrive from a machine this one is nothing like. A stored "mps" on Linux
// resolves to the CPU wheel index and silently replaces a working CUDA torch.
//
// Parameterised rather than reading runtime.GOOS directly so a test on one
// platform can check the answers for every other — the previous arrangement had
// this rule written twice and compared with a host-dependent test, which meant
// a Linux CI run could only ever exercise the Linux column.
func AccelApplies(goos, goarch, accel string) bool {
	switch accel {
	case "reuse", "cpu":
		return true
	case "cuda":
		// No CUDA build of PyTorch exists for macOS.
		return goos != "darwin"
	case "mps":
		// Metal, and only on Apple Silicon: an Intel Mac has no MPS backend.
		return goos == "darwin" && goarch == "arm64"
	}
	return false
}

// DeviceApplies reports whether a separation device is one the named platform
// could ever use. "auto" and "cpu" always are; the accelerators follow the same
// rule as the PyTorch flavour that provides them.
func DeviceApplies(goos, goarch, device string) bool {
	switch device {
	case "auto", "cpu":
		return true
	case "cuda", "mps":
		return AccelApplies(goos, goarch, device)
	}
	return false
}

// AllAccels and AllDevices are every value the install panel and the device
// picker can offer, in the order their dropdowns list them. Kept next to the
// rule that filters them so adding an option cannot quietly skip the gate.
var (
	AllAccels  = []string{"reuse", "cuda", "mps", "cpu"}
	AllDevices = []string{"auto", "cuda", "mps", "cpu"}
)

// AccelsFor and DevicesFor are what a platform may actually be shown. The
// frontend receives these through Bootstrap instead of re-deriving them from
// the platform string, which had made the UI a third copy of a rule the store
// and the installer already shared — and the copy that decides what the user
// can click.
func AccelsFor(goos, goarch string) []string {
	return filter(AllAccels, func(v string) bool { return AccelApplies(goos, goarch, v) })
}

func DevicesFor(goos, goarch string) []string {
	return filter(AllDevices, func(v string) bool { return DeviceApplies(goos, goarch, v) })
}

// Non-nil even when nothing survives, so the value marshals to [] and not null.
func filter(in []string, keep func(string) bool) []string {
	out := []string{}
	for _, v := range in {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

func accelApplies(accel string) bool {
	return AccelApplies(runtime.GOOS, runtime.GOARCH, accel)
}

func deviceApplies(device string) bool {
	return DeviceApplies(runtime.GOOS, runtime.GOARCH, device)
}

// Store is a concurrency-safe holder around the settings file.
type Store struct {
	mu   sync.RWMutex
	data Settings
}

func Load() *Store {
	s := &Store{data: Defaults()}
	raw, err := os.ReadFile(paths.SettingsFile())
	if err != nil {
		return s
	}
	// Decode over the defaults so new fields added by an upgrade keep sane
	// values instead of becoming zero.
	loaded := s.data
	if err := json.Unmarshal(raw, &loaded); err == nil {
		s.data = loaded
	}
	s.normalize()
	return s
}

func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *Store) Set(next Settings) (Settings, error) {
	s.mu.Lock()
	s.data = next
	s.normalizeLocked()
	out := s.data
	s.mu.Unlock()

	if err := paths.EnsureDir(filepath.Dir(paths.SettingsFile())); err != nil {
		return out, err
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return out, err
	}
	return out, os.WriteFile(paths.SettingsFile(), raw, 0o644)
}

func (s *Store) normalize() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.normalizeLocked()
}

func (s *Store) normalizeLocked() {
	d := Defaults()
	if s.data.OutputDir == "" {
		s.data.OutputDir = d.OutputDir
	}
	if !oneOf(s.data.AudioFormat, "wav", "flac", "mp3") {
		s.data.AudioFormat = d.AudioFormat
	}
	if !oneOf(s.data.StemFormat, "wav", "flac", "mp3") {
		s.data.StemFormat = d.StemFormat
	}
	// Device gets the same platform gate as Accel below, and for the same
	// reason: this file travels with the data directory. A device:"mps" copied
	// onto Linux used to survive normalisation and then lose the GPU on every
	// single separation — app.go sees Backend "cuda" != "mps" and falls back to
	// CPU — while the settings panel showed "Tự động", because the UI had
	// removed the option it could not offer. Reset to auto, which is always
	// right: the app resolves it against the accelerator it has verified.
	if !deviceApplies(s.data.Device) {
		s.data.Device = d.Device
	}
	// "" is valid for Accel and means "not chosen yet"; anything else must be a
	// flavour InstallEngines understands.
	if s.data.Accel != "" && !accelApplies(s.data.Accel) {
		// Cleared, not defaulted: empty means "not chosen", which is what makes
		// the UI ask SuggestedAccel for an answer that fits this machine.
		s.data.Accel = ""
	}
	// Only the indexes the install panel still offers. A tag that was valid in
	// an older build — cu121, cu124, cu128 — is migrated to the default rather
	// than kept: the dropdown has no matching option for it, so keeping it left
	// the select blank and the displayed choice disagreeing with the installed
	// one. cu126 covers every card those three did.
	if !oneOf(s.data.CudaTag, "cu118", "cu126", "cu130") {
		s.data.CudaTag = d.CudaTag
	}
	if s.data.ModelID == "" {
		s.data.ModelID = d.ModelID
	}
	if s.data.Shifts < 0 || s.data.Shifts > 20 {
		s.data.Shifts = d.Shifts
	}
	if s.data.Overlap <= 0 || s.data.Overlap >= 1 {
		s.data.Overlap = d.Overlap
	}
	if s.data.Segment < 0 {
		s.data.Segment = 0
	}
	if s.data.Jobs < 1 || s.data.Jobs > 32 {
		s.data.Jobs = d.Jobs
	}
	if s.data.Mp3Bitrate < 64 || s.data.Mp3Bitrate > 320 {
		s.data.Mp3Bitrate = d.Mp3Bitrate
	}
	if s.data.RoformerSegmentSize < 32 {
		s.data.RoformerSegmentSize = d.RoformerSegmentSize
	}
	if s.data.RoformerOverlap < 2 || s.data.RoformerOverlap > 64 {
		s.data.RoformerOverlap = d.RoformerOverlap
	}
	if s.data.RoformerBatchSize < 1 || s.data.RoformerBatchSize > 16 {
		s.data.RoformerBatchSize = d.RoformerBatchSize
	}
	if s.data.Normalization <= 0 || s.data.Normalization > 1 {
		s.data.Normalization = d.Normalization
	}
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
