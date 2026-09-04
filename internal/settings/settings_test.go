package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"demucs-studio/internal/paths"
)

// foreignAccel names a PyTorch flavour this machine cannot use, whatever
// machine it is. Every platform rejects something — Linux has no MPS, macOS has
// no CUDA build at all — so a portable test of the reset has to ask which one
// rather than assume. Hardcoding "cuda" as the *valid* value is what made these
// tests green on Linux and red on a macOS runner, on exactly the platform the
// MPS work was written for.
func foreignAccel() string {
	if AccelApplies(runtime.GOOS, runtime.GOARCH, "cuda") {
		return "mps"
	}
	return "cuda"
}

// foreignDevice is the same idea for the separation device. It is deliberately
// derived from AccelApplies rather than repeating the rule.
func foreignDevice() string { return foreignAccel() }

// nativeAccel is a flavour every platform can install, so a test that only
// wants "some valid choice" does not have to care where it runs.
const nativeAccel = "cpu"

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
	next.Accel = nativeAccel
	next.CudaTag = "cu130"
	if _, err := first.Set(next); err != nil {
		t.Fatal(err)
	}

	// A new Store is what the next launch of the app builds.
	reopened := Load().Get()
	if reopened.Accel != nativeAccel {
		t.Errorf("after restart Accel = %q, want %q", reopened.Accel, nativeAccel)
	}
	if reopened.CudaTag != "cu130" {
		t.Errorf("after restart CudaTag = %q, want cu130", reopened.CudaTag)
	}
}

// The platform gate has to survive a round trip through the file, not merely
// hold inside normalize(): the whole scenario is a settings.json carried from
// another machine, which is read back exactly this way. A stored device the
// platform cannot use had been resolving to CPU on every separation while the
// panel still showed "Tự động", because the UI had removed the option.
func TestForeignChoicesAreResetOnLoad(t *testing.T) {
	t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())

	// Written straight to disk rather than through Set(), which normalises and
	// so cannot produce this state — the file genuinely arrives from elsewhere.
	foreign := Defaults()
	foreign.Accel = foreignAccel()
	foreign.Device = foreignDevice()
	raw, err := json.Marshal(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if err := paths.EnsureDir(filepath.Dir(paths.SettingsFile())); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.SettingsFile(), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	reopened := Load().Get()
	if reopened.Accel != "" {
		t.Errorf("Accel = %q sau khi mở lại, want rỗng để UI hỏi SuggestedAccel", reopened.Accel)
	}
	if reopened.Device != "auto" {
		t.Errorf("Device = %q sau khi mở lại, want auto", reopened.Device)
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
	if def.Device != "auto" {
		t.Errorf("Defaults().Device = %q, want auto", def.Device)
	}

	for _, tc := range []struct {
		name              string
		accel, cudaTag    string
		wantAccel, wantCT string
	}{
		{"giữ lựa chọn hợp lệ", nativeAccel, "cu126", nativeAccel, "cu126"},
		{"giữ cả tag cũ nếu người dùng cố ý chọn", "cpu", "cu118", "cpu", "cu118"},
		{"tag mới hơn vẫn hợp lệ", "reuse", "cu130", "reuse", "cu130"},
		// Empty accel is meaningful — "chưa chọn" — and must survive.
		{"accel rỗng được giữ nguyên", "", "cu126", "", "cu126"},
		{"accel rác bị loại", "gpu", "cu126", "", "cu126"},
		// Not junk, just not installable here — same outcome, different reason.
		{"accel của nền tảng khác bị loại", foreignAccel(), "cu126", "", "cu126"},
		{"tag rác quay về mặc định", "reuse", "cu999", "reuse", "cu126"},
		{"tag rỗng quay về mặc định", "reuse", "", "reuse", "cu126"},
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

func TestNormalizeDevice(t *testing.T) {
	for _, tc := range []struct {
		name, device, want string
	}{
		{"auto giữ nguyên", "auto", "auto"},
		{"cpu giữ nguyên", "cpu", "cpu"},
		{"rác quay về auto", "rocm", "auto"},
		{"rỗng quay về auto", "", "auto"},
		// The reported bug: a device copied in from another platform resolved to
		// CPU on every separation while the panel showed "Tự động".
		{"thiết bị của nền tảng khác quay về auto", foreignDevice(), "auto"},
	} {
		s := &Store{data: Settings{Device: tc.device}}
		s.normalize()
		if s.data.Device != tc.want {
			t.Errorf("%s: Device = %q, want %q", tc.name, s.data.Device, tc.want)
		}
	}

	// And the accelerator this machine does support has to survive, or the
	// reset above would just be a way of ignoring the user's choice.
	if AccelApplies(runtime.GOOS, runtime.GOARCH, "cuda") {
		s := &Store{data: Settings{Device: "cuda"}}
		s.normalize()
		if s.data.Device != "cuda" {
			t.Errorf("Device cuda bị đổi thành %q trên máy dùng được CUDA", s.data.Device)
		}
	}
	if AccelApplies(runtime.GOOS, runtime.GOARCH, "mps") {
		s := &Store{data: Settings{Device: "mps"}}
		s.normalize()
		if s.data.Device != "mps" {
			t.Errorf("Device mps bị đổi thành %q trên Apple Silicon", s.data.Device)
		}
	}
}

// The two gates are one rule, so DeviceApplies must not drift from
// AccelApplies. Checked for every platform, not just the host.
func TestDeviceAppliesFollowsAccelApplies(t *testing.T) {
	for _, p := range [][2]string{
		{"linux", "amd64"}, {"windows", "amd64"},
		{"darwin", "arm64"}, {"darwin", "amd64"},
	} {
		for _, d := range []string{"auto", "cpu"} {
			if !DeviceApplies(p[0], p[1], d) {
				t.Errorf("%s/%s: %q phải luôn dùng được", p[0], p[1], d)
			}
		}
		for _, d := range []string{"cuda", "mps"} {
			want := AccelApplies(p[0], p[1], d)
			if got := DeviceApplies(p[0], p[1], d); got != want {
				t.Errorf("%s/%s: DeviceApplies(%q) = %v, AccelApplies = %v", p[0], p[1], d, got, want)
			}
		}
		if DeviceApplies(p[0], p[1], "rocm") {
			t.Errorf("%s/%s: thiết bị lạ phải bị loại", p[0], p[1])
		}
	}
}

// What the UI may offer has to be exactly what normalisation keeps, or the
// panel shows an option that is silently discarded on the next launch — which
// is how "mps" came to be selectable on Linux.
func TestOfferedValuesAreExactlyTheOnesThatSurvive(t *testing.T) {
	for _, p := range [][2]string{
		{"linux", "amd64"}, {"windows", "amd64"},
		{"darwin", "arm64"}, {"darwin", "amd64"},
	} {
		accels := AccelsFor(p[0], p[1])
		if accels == nil {
			t.Errorf("%s/%s: AccelsFor trả nil — JSON hoá thành null", p[0], p[1])
		}
		for _, a := range AllAccels {
			offered := false
			for _, got := range accels {
				if got == a {
					offered = true
				}
			}
			if offered != AccelApplies(p[0], p[1], a) {
				t.Errorf("%s/%s: %q offered=%v nhưng AccelApplies=%v",
					p[0], p[1], a, offered, AccelApplies(p[0], p[1], a))
			}
		}

		devices := DevicesFor(p[0], p[1])
		if devices == nil {
			t.Errorf("%s/%s: DevicesFor trả nil — JSON hoá thành null", p[0], p[1])
		}
		// Every platform keeps a way to install and a way to run, whatever else
		// it loses; an empty list would leave the panel unusable.
		if len(accels) == 0 || len(devices) == 0 {
			t.Errorf("%s/%s: accels=%v devices=%v, cả hai đều không được rỗng", p[0], p[1], accels, devices)
		}
	}

	// The lists are what the dropdowns render, so their order is the UI's.
	if AllAccels[0] != "reuse" || AllDevices[0] != "auto" {
		t.Errorf("thứ tự mặc định đổi: accels=%v devices=%v", AllAccels, AllDevices)
	}
}
