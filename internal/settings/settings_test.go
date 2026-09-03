package settings

import (
	"testing"
)

// TestEngineChoicesSurviveARestart is the regression test for the reported bug:
// picking "Tải bản CUDA (GPU)" with cu126 and reopening the app showed cu124
// and "Dùng lại bản đã có" again, because neither field was persisted at all.
func TestEngineChoicesSurviveARestart(t *testing.T) {
	t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())

	first := Load()
	// A fresh install must not preselect a CUDA index the user never picked,
	// but it must offer a current one.
	if got := first.Get().CudaTag; got != "cu126" {
		t.Errorf("fresh install CudaTag = %q, want cu126", got)
	}

	next := first.Get()
	next.Accel = "cuda"
	next.CudaTag = "cu130"
	if _, err := first.Set(next); err != nil {
		t.Fatal(err)
	}

	// A new Store is what the next launch of the app builds.
	reopened := Load().Get()
	if reopened.Accel != "cuda" {
		t.Errorf("after restart Accel = %q, want cuda", reopened.Accel)
	}
	if reopened.CudaTag != "cu130" {
		t.Errorf("after restart CudaTag = %q, want cu130", reopened.CudaTag)
	}
}

func TestNormalizeEngineInstallChoices(t *testing.T) {
	def := Defaults()
	if def.Accel != "" {
		t.Errorf("Defaults().Accel = %q, want empty so the UI can ask for a suggestion", def.Accel)
	}
	// The default index has to be one PyTorch still publishes current wheels
	// to; cu124 stopped, which is what pinned Python 3.13 users to torch 2.6.
	if def.CudaTag != "cu126" {
		t.Errorf("Defaults().CudaTag = %q, want cu126", def.CudaTag)
	}

	for _, tc := range []struct {
		name              string
		accel, cudaTag    string
		wantAccel, wantCT string
	}{
		{"giữ lựa chọn hợp lệ", "cuda", "cu126", "cuda", "cu126"},
		{"giữ cả tag cũ nếu người dùng cố ý chọn", "cpu", "cu118", "cpu", "cu118"},
		{"tag mới hơn vẫn hợp lệ", "cuda", "cu130", "cuda", "cu130"},
		// Empty accel is meaningful — "chưa chọn" — and must survive.
		{"accel rỗng được giữ nguyên", "", "cu126", "", "cu126"},
		{"accel rác bị loại", "gpu", "cu126", "", "cu126"},
		{"tag rác quay về mặc định", "cuda", "cu999", "cuda", "cu126"},
		{"tag rỗng quay về mặc định", "cuda", "", "cuda", "cu126"},
	} {
		s := &Store{data: Settings{Accel: tc.accel, CudaTag: tc.cudaTag}}
		s.normalize()
		if s.data.Accel != tc.wantAccel {
			t.Errorf("%s: Accel = %q, want %q", tc.name, s.data.Accel, tc.wantAccel)
		}
		if s.data.CudaTag != tc.wantCT {
			t.Errorf("%s: CudaTag = %q, want %q", tc.name, s.data.CudaTag, tc.wantCT)
		}
	}
}
