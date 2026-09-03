package deps

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"demucs-studio/internal/paths"
)

// A rebuild empties the venv, and the install panel only ever asks for one
// engine — its buttons are rendered per missing-tool row. Without folding the
// resident engines back in, switching PyTorch flavour from the demucs row
// uninstalled audio-separator and vice versa.
func TestWithResidentEngines(t *testing.T) {
	t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())
	inVenv := func(name string) Tool {
		return Tool{Found: true, Path: filepath.Join(paths.VenvDir(), "bin", name)}
	}

	for _, tc := range []struct {
		name      string
		requested []string
		resident  map[string]Tool
		want      []string
	}{
		{
			name:      "engine cùng venv được giữ lại",
			requested: []string{ToolDemucs},
			resident: map[string]Tool{
				ToolDemucs:         inVenv("demucs"),
				ToolAudioSeparator: inVenv("audio-separator"),
			},
			want: []string{ToolDemucs, ToolAudioSeparator},
		},
		{
			// It survives the wipe, so reinstalling it into the venv would be
			// unrequested work and would shadow the copy the user kept.
			name:      "engine ngoài venv bị bỏ qua",
			requested: []string{ToolDemucs},
			resident: map[string]Tool{
				ToolAudioSeparator: {Found: true, Path: "/usr/bin/audio-separator"},
			},
			want: []string{ToolDemucs},
		},
		{
			name:      "engine chưa cài thì không thêm",
			requested: []string{ToolAudioSeparator},
			resident:  map[string]Tool{ToolDemucs: {Found: false}},
			want:      []string{ToolAudioSeparator},
		},
		{
			name:      "không nhân đôi cái đã yêu cầu",
			requested: []string{ToolDemucs, ToolAudioSeparator},
			resident: map[string]Tool{
				ToolDemucs:         inVenv("demucs"),
				ToolAudioSeparator: inVenv("audio-separator"),
			},
			want: []string{ToolDemucs, ToolAudioSeparator},
		},
		{
			name:      "không có gì resident",
			requested: []string{ToolDemucs},
			resident:  nil,
			want:      []string{ToolDemucs},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withResidentEngines(tc.requested, tc.resident)
			if !slices.Equal(got, tc.want) {
				t.Errorf("withResidentEngines = %v, want %v", got, tc.want)
			}
		})
	}
}

// venvCreator is how a rebuild finds an interpreter outside the venv, since the
// resolved one is by definition inside it — asking that one to --clear its own
// directory is what Windows refuses outright.
func TestVenvCreatorReadsTheRecordedInterpreter(t *testing.T) {
	t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())
	if got := venvCreator(); got != "" {
		t.Errorf("no venv yet, want empty, got %q", got)
	}

	writeCfg := func(body string) {
		t.Helper()
		if err := os.MkdirAll(paths.VenvDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(paths.VenvDir(), "pyvenv.cfg"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeCfg("home = /usr/bin\nexecutable = /usr/bin/python3.11\nversion = 3.11.15\n")
	if got := venvCreator(); got != "/usr/bin/python3.11" {
		t.Errorf("venvCreator() = %q, want /usr/bin/python3.11", got)
	}

	// A venv from an older Python that did not record it must not yield a
	// bogus path; the caller falls back to scanning candidates.
	writeCfg("home = /usr/bin\nversion = 3.9.0\n")
	if got := venvCreator(); got != "" {
		t.Errorf("no executable key, want empty, got %q", got)
	}
}
