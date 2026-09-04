package deps

import (
	"runtime"
	"sort"
	"strings"
	"testing"

	"demucs-studio/internal/settings"
)

// testPlatforms is every platform the app ships for, so a check runs all four
// columns instead of only whichever one the CI host happens to be. Shared
// because the same table was being written out per test, and a table copied per
// test is one that gets a new row in five places and not the sixth.
var testPlatforms = []struct{ goos, goarch string }{
	{"linux", "amd64"}, {"windows", "amd64"},
	{"darwin", "arm64"}, {"darwin", "amd64"},
}

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
	targets := cudaTargetsForOS("linux", "amd64", "", "")
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
	for _, tr := range cudaTargetsForOS("linux", "amd64", "", "") {
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
	for _, tr := range cudaTargetsForOS("linux", "amd64", "", "") {
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
	for _, tr := range cudaTargetsForOS("linux", "amd64", "", "") {
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

// macOS has no CUDA index to pick: there is one PyTorch wheel and it already
// contains Metal support, so "GPU or CPU" there is the device passed at
// separation time, not something installed. The UI keys off an empty list to
// hide the picker entirely.
func TestNoCudaIndexesOnMacOS(t *testing.T) {
	// Empty but non-nil, and the distinction is the whole test: this crosses to
	// the frontend as JSON, where a nil slice marshals to null while
	// []CudaTarget{} marshals to []. main.ts reads .length off the payload, so
	// returning nil here is the difference between a quietly hidden picker and
	// a TypeError that aborts the entire deps render on every Mac. len() alone
	// cannot tell the two apart.
	for _, tc := range []struct{ cap, driver string }{
		{"", ""},
		// Even with a capability and driver in hand — a Mac reports neither,
		// but the answer must not depend on that.
		{"8.6", "580.1"},
	} {
		got := cudaTargetsForOS("darwin", "arm64", tc.cap, tc.driver)
		if got == nil {
			t.Errorf("darwin cap=%q driver=%q trả nil — JSON hoá thành null và main.ts sẽ nổ ở .length",
				tc.cap, tc.driver)
		}
		if len(got) != 0 {
			t.Errorf("darwin cap=%q offered %d CUDA indexes, want none", tc.cap, len(got))
		}
	}
	for _, goos := range []string{"linux", "windows"} {
		if len(cudaTargetsForOS(goos, "amd64", "", "")) == 0 {
			t.Errorf("%s must still offer indexes", goos)
		}
	}
}

// A flavour with no build for this platform must be refused, not quietly
// mapped to an index that means something else. "mps" on Linux fell through to
// the CPU wheel index and replaced a working CUDA torch with a CPU one — the
// same damage as the old "reuse" bug, triggered by one option the UI should
// never have offered.
func TestAccelUnavailable(t *testing.T) {
	for _, tc := range []struct {
		goos, goarch, accel string
		wantRefused         bool
		mentions            string
	}{
		// The reported case.
		{"linux", "amd64", "mps", true, "Apple Silicon"},
		{"windows", "amd64", "mps", true, "windows"},
		// Intel Macs have no Metal backend in PyTorch.
		{"darwin", "amd64", "mps", true, "Apple Silicon"},
		{"darwin", "arm64", "mps", false, ""},
		// And there is no CUDA build for macOS at all.
		{"darwin", "arm64", "cuda", true, "macOS"},
		{"darwin", "amd64", "cuda", true, "macOS"},
		{"linux", "amd64", "cuda", false, ""},
		{"windows", "amd64", "cuda", false, ""},
		// These two work everywhere.
		{"darwin", "arm64", "cpu", false, ""},
		{"linux", "amd64", "reuse", false, ""},
		// Anything unrecognised is refused rather than defaulted.
		{"linux", "amd64", "rocm", true, "không hợp lệ"},
	} {
		name := tc.goos + "/" + tc.goarch + " " + tc.accel
		got := accelUnavailable(tc.goos, tc.goarch, tc.accel)
		if tc.wantRefused && got == "" {
			t.Errorf("%s: expected a refusal, got none", name)
		}
		if !tc.wantRefused && got != "" {
			t.Errorf("%s: unexpectedly refused: %q", name, got)
		}
		if tc.mentions != "" && !strings.Contains(got, tc.mentions) {
			t.Errorf("%s: message %q should mention %q", name, got, tc.mentions)
		}
	}
}

// The installer and the settings store enforce the same rule from opposite
// directions — one for a value arriving from the UI, one for a value arriving
// from a file copied off another machine. They must agree on this host, or a
// stored value survives normalisation and is then refused at install time.
//
// Driven through the real store, because accelUnavailable delegates to
// AccelApplies on its very first line: comparing those two compares a function
// with itself. The previous version did exactly that and asserted
// `applies == refused` — which reduces to `x == !x`, false for every possible
// input, so the test could not fail however far the two sides drifted.
//
// Host platform only, and necessarily so: normalize() reads runtime.GOOS. That
// is the honest scope for a store round-trip, and the cross-platform agreement
// of the rule itself is settings.TestOfferedValuesAreExactlyTheOnesThatSurvive.
func TestAccelPlatformRuleMatchesInstaller(t *testing.T) {
	cases := append(append([]string{}, settings.AllAccels...), "rocm", "")
	for _, accel := range cases {
		t.Run("accel="+accel, func(t *testing.T) {
			t.Setenv("DEMUCS_STUDIO_HOME", t.TempDir())
			store := settings.Load()
			next := store.Get()
			next.Accel = accel
			saved, err := store.Set(next)
			if err != nil {
				t.Fatal(err)
			}
			// "" is not a survivor: it is the store's way of saying "not chosen
			// yet", which is also what a rejected value is turned into.
			survives := accel != "" && saved.Accel == accel
			refused := accelUnavailable(runtime.GOOS, runtime.GOARCH, accel) != ""
			if survives == refused {
				t.Errorf("%q: store giữ lại=%v, installer từ chối=%v — "+
					"một giá trị lưu được rồi bị chặn lúc cài (hoặc ngược lại)",
					accel, survives, refused)
			}
		})
	}
}

// suggestedIn reports which flavours a refusal points the user at, read through
// the installer's own accelLabels rather than a copy of the wording. A copy
// could only ever compare the messages against themselves: rename an <option>
// and both the copy and the message would be updated together while the panel
// the user is looking at was not.
func suggestedIn(msg string) []string {
	var out []string
	for accel, label := range accelLabels {
		if strings.Contains(msg, label) {
			out = append(out, accel)
		}
	}
	sort.Strings(out)
	return out
}

// A refusal that sends the user to a second refusal is worse than none, so
// every alternative a message names has to be installable on the machine
// reading it.
//
// This replaces a check that asked whether a refusal had an empty message —
// impossible, since "refused" was defined as the message being non-empty, so it
// could never fire. It was hiding a live bug: an Intel Mac was told to pick
// “Tải bản GPU Apple (MPS)”, which it has no Metal backend for either.
func TestRefusalsOnlySuggestInstallableAlternatives(t *testing.T) {
	for _, p := range testPlatforms {
		for _, accel := range []string{"cuda", "mps", "rocm", ""} {
			msg := accelUnavailable(p.goos, p.goarch, accel)
			if msg == "" {
				continue // installable here; nothing to say.
			}
			alts := suggestedIn(msg)
			for _, alt := range alts {
				if alt == accel {
					t.Errorf("%s/%s %q: gợi ý lại đúng lựa chọn vừa bị từ chối: %q",
						p.goos, p.goarch, accel, msg)
				}
				if !settings.AccelApplies(p.goos, p.goarch, alt) {
					t.Errorf("%s/%s %q: gợi ý %q nhưng máy này cũng không cài được: %q",
						p.goos, p.goarch, accel, alt, msg)
				}
			}
			// "cuda" and "mps" are refused for a platform reason, so there is
			// always a working flavour to point at. A junk value has nothing to
			// correct towards and only needs to echo what it rejected.
			switch accel {
			case "cuda", "mps":
				if len(alts) == 0 {
					t.Errorf("%s/%s %q: từ chối mà không chỉ ra lựa chọn nào dùng được: %q",
						p.goos, p.goarch, accel, msg)
				}
			default:
				if !strings.Contains(msg, "không hợp lệ") {
					t.Errorf("%s/%s %q: giá trị rác phải được nói thẳng là không hợp lệ: %q",
						p.goos, p.goarch, accel, msg)
				}
				// Guarded: Contains(msg, "") is vacuously true, so the empty
				// accel would otherwise pass this without asserting anything.
				if accel != "" && !strings.Contains(msg, accel) {
					t.Errorf("%s/%s %q: từ chối mà không nhắc giá trị bị loại: %q",
						p.goos, p.goarch, accel, msg)
				}
			}
		}
	}
}

// The device list and the accel list have to agree about which accelerators
// exist, or the app offers a device it can never install a backend for.
func TestDeviceRuleFollowsAccelRule(t *testing.T) {
	for _, p := range testPlatforms {
		for _, accel := range []string{"cuda", "mps"} {
			if settings.DeviceApplies(p.goos, p.goarch, accel) != settings.AccelApplies(p.goos, p.goarch, accel) {
				t.Errorf("%s/%s %q: device and accel rules disagree", p.goos, p.goarch, accel)
			}
		}
		// These two are always available and never depend on hardware.
		for _, always := range []string{"auto", "cpu"} {
			if !settings.DeviceApplies(p.goos, p.goarch, always) {
				t.Errorf("%s/%s: %q must always be selectable", p.goos, p.goarch, always)
			}
		}
		if settings.DeviceApplies(p.goos, p.goarch, "rocm") {
			t.Errorf("%s/%s: unknown device accepted", p.goos, p.goarch)
		}
	}
}

// Every refusal in the installer builds its advice with suggestAccels, so
// testing it directly is what covers the "reuse but no torch" error too. That
// one is raised from deep inside InstallEngines, behind a created venv and a
// torch import that no unit test drives — and it was wrong on an Intel Mac for
// exactly as long as nothing reached it: it re-derived the platform with a bare
// GOOS check and offered MPS, which the same file refuses two hundred lines up.
func TestSuggestAccelsOnlyNamesInstallableFlavours(t *testing.T) {
	for _, p := range testPlatforms {
		for _, excluded := range [][]string{nil, {"cuda"}, {"mps"}} {
			msg := suggestAccels(p.goos, p.goarch, excluded...)
			if msg == "" {
				t.Errorf("%s/%s: gợi ý rỗng — câu từ chối sẽ đứt giữa chừng", p.goos, p.goarch)
				continue
			}
			for _, alt := range suggestedIn(msg) {
				if !settings.AccelApplies(p.goos, p.goarch, alt) {
					t.Errorf("%s/%s: gợi ý %q nhưng máy này không cài được: %q",
						p.goos, p.goarch, alt, msg)
				}
				for _, e := range excluded {
					if alt == e {
						t.Errorf("%s/%s: gợi ý lại %q vừa bị loại: %q", p.goos, p.goarch, alt, msg)
					}
				}
				// "reuse" installs no torch, so it can never be the answer to
				// "no usable torch" — the question every caller is asking.
				if alt == "reuse" {
					t.Errorf("%s/%s: gợi ý “dùng lại bản đã có”, vốn không cài torch: %q",
						p.goos, p.goarch, msg)
				}
			}
			// CPU works everywhere, so there is always something to name.
			if len(suggestedIn(msg)) == 0 {
				t.Errorf("%s/%s: không nêu được lựa chọn nào: %q", p.goos, p.goarch, msg)
			}
		}
	}
}

// The labels the messages are built from have to be the ones on screen. Nothing
// in Go can read index.html, so this at least pins the set of values: a flavour
// the panel offers with no label would produce a refusal that names it as "".
func TestAccelLabelsCoverEveryOfferedFlavour(t *testing.T) {
	for _, accel := range settings.AllAccels {
		if accelLabels[accel] == "" {
			t.Errorf("thiếu nhãn cho flavour %q — câu gợi ý sẽ bỏ trống nó", accel)
		}
	}
	for accel := range accelLabels {
		if !settings.AccelApplies("linux", "amd64", accel) &&
			!settings.AccelApplies("darwin", "arm64", accel) {
			t.Errorf("nhãn %q không ứng với flavour nào cài được ở đâu cả", accel)
		}
	}
}
