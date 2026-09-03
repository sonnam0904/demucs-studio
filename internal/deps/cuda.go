package deps

import (
	"fmt"
	"strconv"
	"strings"
)

// This file answers one question for the UI: given a PyTorch CUDA index, which
// graphics cards can actually run it?
//
// A wheel only contains machine code for the compute capabilities it was
// compiled for, and the set differs sharply between indexes — cu130 dropped
// everything below 7.5, while cu126 still carries 5.0. Picking the wrong index
// leaves the user with a torch that imports, reports CUDA as available, and
// then has no kernel to run. The lists below are not guesses: each was read out
// of the actual wheel with `cuobjdump --list-elf libtorch_cuda.so`.

// CudaTarget is one selectable PyTorch CUDA index.
type CudaTarget struct {
	Tag string `json:"tag"`
	// Torch is the newest release the index carries, which is not the same for
	// all of them: cu118 stopped receiving new builds long before cu126 did.
	Torch string `json:"torch"`
	// Driver describes the NVIDIA driver this index needs, for display, and
	// MinDriver is the same requirement as a number so Supported can enforce
	// it. Arch coverage alone is not enough: cu130 carries sm_120 kernels for
	// an RTX 50xx, but CUDA 13 will not load at all under a 12.x driver, so
	// checking only the arch told such a user "✓ works" and then failed after a
	// multi-gigabyte download.
	Driver    string  `json:"driver"`
	MinDriver float64 `json:"minDriver"`
	// Arches is the compiled compute-capability list, verified per wheel.
	Arches []string `json:"arches"`
	// Families are the card generations Arches can drive, derived rather than
	// hand-written so the two can never disagree.
	Families    []GPUFamily `json:"families"`
	Recommended bool        `json:"recommended"`
	// Supported answers "will this index drive this machine?". False whenever
	// the capability is unknown, so the UI never claims a fit it has not
	// established — and Blocker then says which requirement failed, empty when
	// the answer is simply not known. A bare false was not enough: the panel
	// has to distinguish "no kernels for your card" from "driver too old",
	// because the remedies differ.
	Supported bool   `json:"supported"`
	Blocker   string `json:"blocker"`
}

// GPUFamily is a card generation, as a user would recognise it.
type GPUFamily struct {
	Name  string `json:"name"`
	Cards string `json:"cards"`
	// Caps are the compute capabilities that ship in this generation. Input to
	// familiesFor only, never sent to the UI — it renders Name and Cards.
	Caps []string `json:"-"`
}

// gpuFamilies is ordered oldest first, which is also how the UI lists them.
var gpuFamilies = []GPUFamily{
	{"Kepler", "Tesla K80", []string{"3.7"}},
	{"Maxwell", "GTX 750 Ti, GTX 950/960/970/980, Titan X", []string{"5.0", "5.2", "5.3"}},
	{"Pascal", "GTX 1050–1080 Ti, Titan Xp, Tesla P100", []string{"6.0", "6.1", "6.2"}},
	{"Volta", "Titan V, Tesla V100", []string{"7.0", "7.2"}},
	{"Turing", "GTX 1650/1660 Ti, RTX 2060–2080 Ti", []string{"7.5"}},
	{"Ampere", "RTX 3050–3090 Ti, A100, A40", []string{"8.0", "8.6", "8.7"}},
	{"Ada", "RTX 4060–4090", []string{"8.9"}},
	{"Hopper", "H100, H200", []string{"9.0"}},
	{"Blackwell", "RTX 5060–5090, B100, B200", []string{"10.0", "12.0"}},
}

// cudaIndexes carries only the indexes worth offering. cu121 and cu124 are
// deliberately absent: PyTorch stopped publishing new builds to them, so cu124
// resolves to torch 2.6 on a current Python while cu126 reaches 2.14, and cu126
// covers every card cu124 did. cu128 is likewise superseded by cu130 for the
// same card set.
var cudaIndexes = []CudaTarget{
	{
		Tag: "cu126", Torch: "2.14", Driver: "12.x (≥ 525)", MinDriver: 525,
		Arches:      []string{"sm_50", "sm_60", "sm_70", "sm_75", "sm_80", "sm_86", "sm_89", "sm_90"},
		Recommended: true,
	},
	{
		Tag: "cu130", Torch: "2.14", Driver: "≥ 580", MinDriver: 580,
		Arches: []string{"sm_75", "sm_80", "sm_86", "sm_90", "sm_100", "sm_120"},
	},
	{
		Tag: "cu118", Torch: "2.7.1", Driver: "11.x (≥ 450)", MinDriver: 450,
		Arches: []string{"sm_37", "sm_50", "sm_60", "sm_70", "sm_75", "sm_80", "sm_86", "sm_90"},
	},
}

// CudaTargetsFor returns the selectable indexes, each with its card list and a
// verdict for the machine described by capability and driver, so the install
// panel can say which choices actually fit instead of leaving the user to read
// compute capabilities off a table. Empty arguments mean "unknown", which
// yields no verdict rather than a negative one.
func CudaTargetsFor(capability, driver string) []CudaTarget {
	out := make([]CudaTarget, 0, len(cudaIndexes))
	for _, t := range cudaIndexes {
		t.Families = familiesFor(t.Arches)
		t.Supported, t.Blocker = t.fits(capability, driver)
		out = append(out, t)
	}
	return out
}

// fits reports whether this index can drive the described machine and, when it
// cannot, which requirement failed.
func (t CudaTarget) fits(capability, driver string) (bool, string) {
	if capability == "" {
		return false, ""
	}
	if !ArchesCover(t.Arches, capability) {
		return false, "không có kernel cho compute " + capability
	}
	// Only a driver we could both read and parse can veto: an unknown driver
	// must not turn a supported card into an unsupported one.
	if t.MinDriver > 0 && driver != "" {
		if major, ok := driverMajor(driver); ok && major < t.MinDriver {
			return false, fmt.Sprintf("cần driver ≥ %.0f, máy đang %s", t.MinDriver, driver)
		}
	}
	return true, ""
}

// driverMajor pulls the leading number out of a driver version like
// "580.173.02", which is the part NVIDIA bumps for a new CUDA major.
func driverMajor(driver string) (float64, bool) {
	head, _, _ := strings.Cut(driver, ".")
	n, err := strconv.ParseFloat(head, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// familiesFor lists the generations at least one of arches can drive.
func familiesFor(arches []string) []GPUFamily {
	var out []GPUFamily
	for _, f := range gpuFamilies {
		for _, cap := range f.Caps {
			if ArchesCover(arches, cap) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// ArchesCover reports whether any arch in the list produces code the card with
// this compute capability can execute.
//
// The rule is the one PyTorch states in its own mismatch warning — "5.0 which
// supports hardware CC >=5.0,<6.0": a cubin built for sm_XY runs on any card of
// the same major version whose minor is equal or higher. Requiring an exact
// match instead is what made the app reject a GTX 960 (5.2) against a build
// carrying sm_50, a card it could drive perfectly well.
func ArchesCover(arches []string, capability string) bool {
	major, minor, ok := splitCapability(capability)
	if !ok {
		return false
	}
	for _, arch := range arches {
		aMajor, aMinor, ok := splitArch(arch)
		if ok && aMajor == major && aMinor <= minor {
			return true
		}
	}
	return false
}

// splitCapability parses "5.2" into 5 and 2.
func splitCapability(s string) (int, int, bool) {
	head, tail, ok := strings.Cut(s, ".")
	if !ok {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(head)
	minor, err2 := strconv.Atoi(tail)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// splitArch parses "sm_86" into 8 and 6, and "sm_120" into 12 and 0. The last
// digit is always the minor: NVIDIA writes compute capability 12.0 as sm_120.
func splitArch(s string) (int, int, bool) {
	digits, ok := strings.CutPrefix(s, "sm_")
	if !ok || len(digits) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(digits[:len(digits)-1])
	minor, err2 := strconv.Atoi(digits[len(digits)-1:])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}
