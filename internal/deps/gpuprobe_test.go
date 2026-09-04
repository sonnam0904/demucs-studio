package deps

import (
	"os/exec"
	"strings"
	"testing"
)

// The probe is a Python program living in a Go string literal, so the Go
// compiler cannot see a syntax error in it and DetectGPU swallows one at
// runtime into a generic "không đọc được kết quả" — every machine, including a
// working NVIDIA box, then silently reports CPU-only.
//
// The substring assertions elsewhere in this package cannot catch that: a stray
// indent or a typo'd attribute keeps every substring intact. This parses it.
func TestGpuProbeIsValidPython(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("cần python3 để kiểm cú pháp probe")
	}
	cmd := exec.Command(python, "-c", "import ast,sys; ast.parse(sys.stdin.read())")
	cmd.Stdin = strings.NewReader(gpuProbe)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gpuProbe không phải Python hợp lệ: %v\n%s", err, out)
	}
}

// The probe must not report a backend it has not proved. Both branches used to
// assign it before the smoke test, so a failed probe advertised the very
// backend it had just shown to be unusable — contradicting the GPU doc comment,
// types.ts and docs/engineering.md, all of which promise the opposite.
func TestGpuProbeAssignsBackendOnlyAfterSmoke(t *testing.T) {
	for _, backend := range []string{"cuda", "mps"} {
		smoke := "smoke(\"" + backend + "\")"
		assign := "out[\"backend\"] = \"" + backend + "\""
		si, ai := strings.Index(gpuProbe, smoke), strings.Index(gpuProbe, assign)
		if si < 0 || ai < 0 {
			t.Fatalf("%s: thiếu smoke hoặc gán backend trong probe", backend)
		}
		if ai < si {
			t.Errorf("%s: gán backend ở vị trí %d, trước smoke ở %d — một probe hỏng vẫn sẽ báo backend",
				backend, ai, si)
		}
	}
}

// The MPS branch must leave the name to the Go side, which asks the OS for the
// chip. Hardcoding one here made appleChip unreachable and rendered the badge
// as the stuttering "GPU · GPU tích hợp Apple (Metal)".
func TestGpuProbeLeavesAppleNamingToGo(t *testing.T) {
	// Counted rather than searched for a literal, so the assertion tracks the
	// code and not the prose around it: exactly one assignment, and it is the
	// CUDA one, which the arch-mismatch message needs before the smoke test.
	if got := strings.Count(gpuProbe, `out["name"] =`); got != 1 {
		t.Errorf(`probe gán out["name"] %d lần, muốn đúng 1 (chỉ nhánh CUDA)`, got)
	}
	if !strings.Contains(gpuProbe, `out["name"] = torch.cuda.get_device_name(0)`) {
		t.Error("assignment còn lại phải là tên card CUDA")
	}
}

// torch.mps exists only from torch 2.0 while the is_available() gate dates to
// 1.12, so a reused older torch on an M1 would raise AttributeError and be
// reported as having no GPU at all.
func TestGpuProbeGuardsTheDeviceSynchronize(t *testing.T) {
	if strings.Contains(gpuProbe, "torch.mps.synchronize()") {
		t.Error("torch.mps.synchronize() gọi trực tiếp — cần getattr để chịu được torch < 2.0")
	}
	if !strings.Contains(gpuProbe, `getattr(getattr(torch, device, None), "synchronize", None)`) {
		t.Error("thiếu lookup synchronize theo tên device")
	}
}

// Because that synchronize is conditional, it cannot be the only thing making a
// failure observable: every op in the smoke test has to read a value back to
// the host, which forces completion on its own. Discarding the convolution's
// result meant a conv that fails at command-buffer commit went unseen on any
// torch without the device submodule, and the probe called the backend
// verified.
func TestGpuProbeConsumesEverySmokeResult(t *testing.T) {
	raw, _, ok := strings.Cut(gpuProbe, "\nout = {")
	if !ok {
		t.Fatal("không tách được thân smoke() khỏi probe")
	}
	// Comments stripped first: counting them too is how an assertion ends up
	// tracking the prose instead of the code, which this very test did on its
	// first run because the fix's own comment mentions .item().
	var body strings.Builder
	for _, line := range strings.Split(raw, "\n") {
		if code, _, _ := strings.Cut(line, "#"); strings.TrimSpace(code) != "" {
			body.WriteString(code + "\n")
		}
	}
	code := body.String()

	// Two ops, two round-trips to the host.
	if got := strings.Count(code, ".item()"); got != 2 {
		t.Errorf("smoke() có %d lần .item(), muốn 2 — mỗi phép phải được đọc về host\n%s", got, code)
	}
	convAt := strings.Index(code, "conv1d")
	if convAt < 0 {
		t.Fatal("không tìm thấy conv1d trong smoke()")
	}
	if !strings.Contains(code[convAt:], ".item()") {
		t.Error("kết quả conv1d không được tiêu thụ — một conv hỏng sẽ không bị phát hiện")
	}
}
