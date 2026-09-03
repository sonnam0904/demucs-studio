package deps

import (
	"strings"
	"testing"

	"demucs-studio/internal/settings"
)

func TestArchesCoverFollowsTheMinorVersionRule(t *testing.T) {
	cu126 := []string{"sm_50", "sm_60", "sm_70", "sm_75", "sm_80", "sm_86", "sm_89", "sm_90"}
	cu130 := []string{"sm_75", "sm_80", "sm_86", "sm_90", "sm_100", "sm_120"}

	for _, tc := range []struct {
		name    string
		arches  []string
		cap     string
		covered bool
	}{
		// The case that started all this: a GTX 960 is 5.2, and no wheel ever
		// carries sm_52 — it is sm_50 code that runs on it.
		{"GTX 960 trên cu126", cu126, "5.2", true},
		{"GTX 960 trên cu130", cu130, "5.2", false},
		// Exact hits.
		{"RTX 3080 (8.6) trên cu126", cu126, "8.6", true},
		{"RTX 4090 (8.9) trên cu126", cu126, "8.9", true},
		// sm_86 covers 8.9, so an index without sm_89 still drives Ada.
		{"RTX 4090 qua sm_86", []string{"sm_80", "sm_86"}, "8.9", true},
		// But never downwards: sm_86 code will not run on an 8.0 card.
		{"A100 (8.0) không chạy được sm_86", []string{"sm_86"}, "8.0", false},
		// Majors never mix.
		{"Blackwell 12.0 cần sm_120", cu130, "12.0", true},
		{"Blackwell 12.0 trên cu126", cu126, "12.0", false},
		{"Kepler 3.7 trên cu126", cu126, "3.7", false},
		{"Kepler 3.7 trên cu118", []string{"sm_37", "sm_50"}, "3.7", true},
	} {
		if got := ArchesCover(tc.arches, tc.cap); got != tc.covered {
			t.Errorf("%s: ArchesCover(%v, %q) = %v, want %v", tc.name, tc.arches, tc.cap, got, tc.covered)
		}
	}

	// Malformed input must not be reported as supported.
	for _, bad := range []string{"", "5", "abc", "5.x", "."} {
		if ArchesCover(cu126, bad) {
			t.Errorf("ArchesCover accepted malformed capability %q", bad)
		}
	}
	if ArchesCover([]string{"sm_", "smx", "", "sm_x"}, "5.2") {
		t.Error("malformed arch names must not match")
	}
}

func TestCudaTargetsDescribeTheRightCards(t *testing.T) {
	targets := CudaTargetsFor("", "")
	if len(targets) == 0 {
		t.Fatal("no CUDA targets offered")
	}

	byTag := map[string]CudaTarget{}
	for _, tr := range targets {
		byTag[tr.Tag] = tr
		if len(tr.Families) == 0 {
			t.Errorf("%s lists no compatible cards", tr.Tag)
		}
		if tr.Torch == "" || tr.Driver == "" {
			t.Errorf("%s is missing its torch/driver note", tr.Tag)
		}
	}

	// Exactly one default, and it must be the one that both carries current
	// torch builds and runs on any 12.x driver.
	var recommended []string
	for _, tr := range targets {
		if tr.Recommended {
			recommended = append(recommended, tr.Tag)
		}
	}
	if len(recommended) != 1 || recommended[0] != "cu126" {
		t.Errorf("recommended = %v, want exactly [cu126]", recommended)
	}

	families := func(tag string) string {
		var names []string
		for _, f := range byTag[tag].Families {
			names = append(names, f.Name)
		}
		return strings.Join(names, ",")
	}

	// cu126 still ships Maxwell kernels; cu130 does not. This is the exact
	// distinction that decides whether an old card works, so it is asserted
	// rather than left to the data.
	if !strings.Contains(families("cu126"), "Maxwell") {
		t.Errorf("cu126 families = %s, want Maxwell included", families("cu126"))
	}
	if strings.Contains(families("cu130"), "Maxwell") {
		t.Errorf("cu130 families = %s, want Maxwell excluded", families("cu130"))
	}
	if !strings.Contains(families("cu130"), "Blackwell") {
		t.Errorf("cu130 families = %s, want Blackwell included", families("cu130"))
	}
	if strings.Contains(families("cu126"), "Blackwell") {
		t.Errorf("cu126 families = %s, want Blackwell excluded", families("cu126"))
	}
	// Only cu118 goes back as far as Kepler.
	if !strings.Contains(families("cu118"), "Kepler") {
		t.Errorf("cu118 families = %s, want Kepler included", families("cu118"))
	}

	// Families must stay in the oldest-first order the UI renders.
	for _, tr := range targets {
		last := -1
		for _, f := range tr.Families {
			idx := -1
			for i, known := range gpuFamilies {
				if known.Name == f.Name {
					idx = i
				}
			}
			if idx <= last {
				t.Errorf("%s families are out of order at %q", tr.Tag, f.Name)
			}
			last = idx
		}
	}
}

// Every tag the panel offers must survive a settings round-trip, or the saved
// choice silently becomes something else on the next launch.
//
// Driven through the real store rather than a copy of its whitelist: the
// previous version of this test asserted a hardcoded map against another
// hardcoded map, so it could not detect the drift it existed to catch.
func TestCudaTargetTagsSurviveSettingsRoundTrip(t *testing.T) {
	for _, tr := range CudaTargetsFor("", "") {
		t.Run(tr.Tag, func(t *testing.T) {
			t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())
			store := settings.Load()
			next := store.Get()
			next.CudaTag = tr.Tag
			saved, err := store.Set(next)
			if err != nil {
				t.Fatal(err)
			}
			if saved.CudaTag != tr.Tag {
				t.Errorf("offered %q but settings normalised it to %q", tr.Tag, saved.CudaTag)
			}
		})
	}
}

// A tag from an older build has no <option> in the dropdown, so keeping it
// would leave the select blank and the display disagreeing with what gets
// installed. It must be migrated to something offered.
func TestRetiredCudaTagsAreMigrated(t *testing.T) {
	offered := map[string]bool{}
	for _, tr := range CudaTargetsFor("", "") {
		offered[tr.Tag] = true
	}
	for _, retired := range []string{"cu121", "cu124", "cu128", "cu999", ""} {
		t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())
		store := settings.Load()
		next := store.Get()
		next.CudaTag = retired
		saved, err := store.Set(next)
		if err != nil {
			t.Fatal(err)
		}
		if !offered[saved.CudaTag] {
			t.Errorf("retired tag %q normalised to %q, which the panel does not offer",
				retired, saved.CudaTag)
		}
	}
}

// A card with the right kernels but too old a driver must not be reported as a
// fit: CUDA 13 will not load under a 12.x driver, so checking arch coverage
// alone promised an RTX 50xx owner "✓ works" and then failed after a
// multi-gigabyte download.
func TestFitsRequiresTheDriverToo(t *testing.T) {
	var cu130, cu126 CudaTarget
	for _, tr := range CudaTargetsFor("", "") {
		switch tr.Tag {
		case "cu130":
			cu130 = tr
		case "cu126":
			cu126 = tr
		}
	}

	// RTX 5070: compute 12.0, so cu130 has the kernels — but not on driver 550.
	if ok, blocker := cu130.fits("12.0", "580.173.02"); !ok {
		t.Errorf("driver 580 meets cu130's minimum, got blocked by %q", blocker)
	}
	ok, blocker := cu130.fits("12.0", "550.144.03")
	if ok {
		t.Error("cu130 needs driver >= 580 and must not accept 550")
	}
	if !strings.Contains(blocker, "580") || !strings.Contains(blocker, "550.144.03") {
		t.Errorf("blocker should name both the requirement and the installed driver, got %q", blocker)
	}

	// An arch miss is reported as such, not as a driver problem.
	if _, blocker := cu130.fits("5.2", "580.173.02"); !strings.Contains(blocker, "compute 5.2") {
		t.Errorf("arch mismatch blocker = %q, want it to name the capability", blocker)
	}

	// An unreadable driver must not veto a card the arches do cover: unknown is
	// not the same as too old.
	if ok, _ := cu126.fits("5.2", ""); !ok {
		t.Error("an unknown driver must not turn a supported card unsupported")
	}
	if ok, _ := cu126.fits("5.2", "[Not Supported]"); !ok {
		t.Error("an unparseable driver must not veto a supported card")
	}

	// Unknown capability yields no verdict at all — neither a fit nor a blocker
	// the UI could show as "✗ does not work".
	gotOK, gotBlocker := cu126.fits("", "580.173.02")
	if gotOK || gotBlocker != "" {
		t.Errorf("unknown capability = (%v, %q), want (false, \"\") so the UI claims nothing", gotOK, gotBlocker)
	}
}

func TestDriverMajor(t *testing.T) {
	for in, want := range map[string]float64{
		"580.173.02": 580,
		"550.144":    550,
		"450":        450,
	} {
		got, ok := driverMajor(in)
		if !ok || got != want {
			t.Errorf("driverMajor(%q) = (%v, %v), want (%v, true)", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "[Not Supported]", "N/A", "abc.1"} {
		if _, ok := driverMajor(bad); ok {
			t.Errorf("driverMajor(%q) should not parse", bad)
		}
	}
}
