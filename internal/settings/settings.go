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
		OutputDir:           paths.DefaultOutputDir(),
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
	if !oneOf(s.data.Device, "auto", "cuda", "cpu") {
		s.data.Device = d.Device
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
