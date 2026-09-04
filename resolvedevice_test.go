package main

import (
	"testing"

	"demucs-studio/internal/deps"
)

func TestResolveDevice(t *testing.T) {
	cuda := deps.GPU{Checked: true, Available: true, Backend: "cuda", Name: "GTX 960"}
	mps := deps.GPU{Checked: true, Available: true, Backend: "mps", Name: "Apple M2"}
	none := deps.GPU{Checked: true, Reason: "torch không thấy CUDA"}

	for _, tc := range []struct {
		name       string
		preference string
		gpu        deps.GPU
		want       string
	}{
		// auto follows the verified backend, never a hardcoded "cuda" — that is
		// the whole point of the MPS work.
		{"auto trên máy CUDA", "auto", cuda, "cuda"},
		{"auto trên Apple Silicon", "auto", mps, "mps"},
		{"auto khi không có GPU", "auto", none, "cpu"},

		// A stored choice must match the backend that actually works here;
		// settings travel with the data directory between machines.
		{"chọn cuda trên máy CUDA", "cuda", cuda, "cuda"},
		{"chọn mps trên Apple Silicon", "mps", mps, "mps"},
		{"chọn mps trên máy CUDA", "mps", cuda, "cpu"},
		{"chọn cuda trên Apple Silicon", "cuda", mps, "cpu"},
		{"chọn cuda khi không có GPU", "cuda", none, "cpu"},

		{"chọn cpu", "cpu", cuda, "cpu"},

		// Available without a backend must not yield an empty device: every
		// consumer silently drops one, handing the choice back to the engine —
		// exactly what resolving explicitly exists to prevent.
		{"available nhưng backend rỗng", "auto",
			deps.GPU{Checked: true, Available: true}, "cpu"},

		// Junk from a hand-edited settings file.
		{"device lạ", "rocm", cuda, "cpu"},
		{"device rỗng", "", cuda, "cpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := resolveDevice(tc.preference, tc.gpu)
			if got != tc.want {
				t.Errorf("resolveDevice(%q, backend=%q) = %q, want %q",
					tc.preference, tc.gpu.Backend, got, tc.want)
			}
		})
	}
}

// Picking CPU is a plain choice, not an occasion for a caveat about some other
// engine. Whether a backend can honour it is that backend's own business, and
// roformer says so from inside its own runner — see
// TestRoformerWarnsThatCPUIsIgnoredOnAppleSilicon. Gating it here meant gating
// on gpu.Backend == "mps", which is only set once the probe passes, so the
// warning went missing on exactly the broken-MPS Mac that needed it.
func TestResolveDeviceKeepsCPUQuiet(t *testing.T) {
	for _, gpu := range []deps.GPU{
		{Checked: true, Available: true, Backend: "mps", Name: "Apple M2"},
		{Checked: true, Available: true, Backend: "cuda", Name: "GTX 960"},
	} {
		if _, notes := resolveDevice("cpu", gpu); len(notes) != 0 {
			t.Errorf("backend=%q: chọn CPU không cần ghi chú gì, được: %+v", gpu.Backend, notes)
		}
	}
}
