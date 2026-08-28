package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTqdm(t *testing.T) {
	tests := []struct {
		name string
		line string
		want float64
		ok   bool
	}{
		{
			name: "demucs bar",
			line: " 34%|███▍      | 61.0/179.0 [00:20<00:39,  3.0seconds/s]",
			want: 0.34,
			ok:   true,
		},
		{
			name: "audio-separator bar",
			line: " 62%|██████▏   | 8/13 [01:35<00:59, 11.97s/it]",
			want: 0.62,
			ok:   true,
		},
		{
			name: "fractional percent",
			line: "  7.5%|▊         | 1/13 [00:12<02:26, 12.25s/it]",
			want: 0.075,
			ok:   true,
		},
		{
			name: "zero",
			line: "  0%|          | 0.00/3.54k [00:00<?, ?iB/s]",
			want: 0,
			ok:   true,
		},
		{
			name: "complete",
			line: "100%|██████████| 13/13 [02:36<00:00, 12.00s/it]",
			want: 1,
			ok:   true,
		},
		// The guard that matters: prose mentioning a percentage must not be
		// mistaken for progress and yank the bar backwards.
		{
			name: "prose with a percentage",
			line: "INFO - separator - Reduced bleed by 20% compared to the previous model",
			want: 0,
			ok:   false,
		},
		{
			name: "plain log line",
			line: "Separated tracks will be stored in /tmp/out/htdemucs_ft",
			want: 0,
			ok:   false,
		},
		{
			name: "percentage over 100 is not progress",
			line: "warning: gain set to 240% |",
			want: 0,
			ok:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseTqdm(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (line %q)", ok, tc.ok, tc.line)
			}
			if ok && absDiff(got, tc.want) > 1e-9 {
				t.Errorf("fraction = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTqdmTail(t *testing.T) {
	line := " 62%|██████▏   | 8/13 [01:35<00:59, 11.97s/it]"
	if got, want := TqdmTail(line), "01:35<00:59, 11.97s/it"; got != want {
		t.Errorf("TqdmTail = %q, want %q", got, want)
	}
	if got := TqdmTail("no brackets here"); got != "" {
		t.Errorf("TqdmTail = %q, want empty", got)
	}
}

func TestSplitID(t *testing.T) {
	tests := []struct{ in, backend, name string }{
		{"demucs:htdemucs_ft", BackendDemucs, "htdemucs_ft"},
		{"roformer:BS-Roformer-SW.ckpt", BackendRoformer, "BS-Roformer-SW.ckpt"},
		// Legacy unprefixed ids were plain demucs model names.
		{"htdemucs", BackendDemucs, "htdemucs"},
	}
	for _, tc := range tests {
		backend, name := SplitID(tc.in)
		if backend != tc.backend || name != tc.name {
			t.Errorf("SplitID(%q) = %q, %q; want %q, %q",
				tc.in, backend, name, tc.backend, tc.name)
		}
	}
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestResetOutputDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "htdemucs_ft")

	// Simulate a previous run: all stems as WAV.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"vocals.wav", "drums.wav", "bass.wav", "other.wav"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := ResetOutputDir(dir); err != nil {
		t.Fatalf("ResetOutputDir: %v", err)
	}

	// The directory must exist and be empty, or the next run's listing picks up
	// the previous run's stems and reports them as fresh output.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory still holds %v", names)
	}
}

func TestResetOutputDirCreatesMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b", "model")
	if err := ResetOutputDir(dir); err != nil {
		t.Fatalf("ResetOutputDir: %v", err)
	}
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		t.Fatalf("expected the directory to be created: %v", err)
	}
}

// Only the model's own subdirectory is cleared, so a different model's results
// for the same track stay available for comparison.
func TestResetOutputDirLeavesSiblings(t *testing.T) {
	job := t.TempDir()
	mine := filepath.Join(job, "htdemucs_ft")
	other := filepath.Join(job, "mel_band_roformer_kim_ft2_unwa")
	for _, d := range []string{mine, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "vocals.wav"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ResetOutputDir(mine); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(other, "vocals.wav")); err != nil {
		t.Errorf("sibling model's result was destroyed: %v", err)
	}
}
