package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0}, // the tag carries a v, the ldflag does not
		{"1.2.4", "1.2.3", 1},
		{"1.3.0", "1.2.9", 1},
		{"2.0.0", "1.99.99", 1},
		{"1.2.3", "1.2.4", -1},
		{"1.10.0", "1.9.0", 1}, // numeric, not lexicographic
		{"1.2.3+build.7", "1.2.3", 0},
		// Semver prerelease ordering: a prerelease precedes its release.
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.1", "1.0.0-rc.2", -1},
		// "dev" is unparseable and must sort below every real release, so a dev
		// build is never told it is newer than what GitHub published.
		{"dev", "0.0.1", -1},
		{"0.0.1", "dev", 1},
		{"dev", "dev", 0},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseVersionRejectsNonReleases(t *testing.T) {
	// Anything that parses here would be offered updates; anything that does
	// not is treated as a dev build and left alone.
	for _, s := range []string{"dev", "", "1.2", "1.2.3.4", "x.y.z", "1.2.beta", "-1.2.3"} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("parseVersion(%q) accepted, want rejected", s)
		}
	}
	for _, s := range []string{"1.2.3", "v1.2.3", "0.0.0", "1.2.3-rc.1", "1.2.3+meta"} {
		if _, ok := parseVersion(s); !ok {
			t.Errorf("parseVersion(%q) rejected, want accepted", s)
		}
	}
}

func TestAssetSuffixMatchesReleaseNames(t *testing.T) {
	// These must stay in step with what the Makefile names its artifacts;
	// a mismatch means the update button finds no asset to download.
	for _, tc := range []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "-linux-amd64.tar.gz"},
		{"windows", "amd64", "-windows-amd64.zip"},
		{"darwin", "amd64", "-macos-universal.dmg"},
		{"darwin", "arm64", "-macos-universal.dmg"},
	} {
		got, err := assetSuffix(tc.goos, tc.goarch)
		if err != nil {
			t.Errorf("%s/%s: %v", tc.goos, tc.goarch, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s/%s: suffix = %q, want %q", tc.goos, tc.goarch, got, tc.want)
		}
	}
	// No Linux arm64 package is published, so the UI must not offer a download.
	if _, err := assetSuffix("linux", "arm64"); err == nil {
		t.Error("linux/arm64 should have no asset")
	}
}

func TestDetectInstallLinuxSeparatesPortableFromPackaged(t *testing.T) {
	// Portable: run.sh sits next to the binary, as the .tar.gz lays it out.
	root := filepath.Join(t.TempDir(), "demucs-studio")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "demucs-studio")
	for _, f := range []string{exe, filepath.Join(root, "run.sh")} {
		if err := os.WriteFile(f, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := detectInstallFor("linux", exe)
	if got.kind != kindPortable {
		t.Fatalf("kind = %v, want kindPortable", got.kind)
	}
	if got.root != root {
		t.Errorf("root = %q, want %q", got.root, root)
	}
	if got.parent != filepath.Dir(root) {
		t.Errorf("parent = %q, want %q", got.parent, filepath.Dir(root))
	}

	// Packaged: the .deb installs a bare binary into /usr/bin with no run.sh.
	// Misreading this as portable would mean trying to replace /usr/bin itself.
	packaged := detectInstallFor("linux", "/usr/bin/demucs-studio")
	if packaged.kind != kindManaged {
		t.Fatalf("kind = %v, want kindManaged", packaged.kind)
	}
	if ok, reason := packaged.updatable(); ok || reason == "" {
		t.Errorf("packaged install reported updatable=%v reason=%q", ok, reason)
	}
	if packaged.root != "" {
		t.Errorf("a packaged install must expose no root to swap, got %q", packaged.root)
	}

	// A bare binary outside any system prefix is not a package install, and
	// must not be described as one — a `go build` output lands here.
	loose := filepath.Join(t.TempDir(), "demucs-studio")
	if err := os.WriteFile(loose, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if k := detectInstallFor("linux", loose).kind; k != kindUnknown {
		t.Errorf("loose linux binary kind = %v, want kindUnknown", k)
	}
}

func TestUnderSystemPrefix(t *testing.T) {
	for _, dir := range []string{"/usr/bin", "/usr/local/bin", "/opt/demucs", "/snap/x/1/bin", "/bin"} {
		if !underSystemPrefix(dir) {
			t.Errorf("%q should count as a package-manager location", dir)
		}
	}
	// "/usrlocal" shares a prefix with "/usr" as a string but is not under it.
	for _, dir := range []string{"/home/u/demucs-studio", "/tmp/go-build123/b001", "/usrlocal/bin", "/"} {
		if underSystemPrefix(dir) {
			t.Errorf("%q should not count as a package-manager location", dir)
		}
	}
}

func TestDetectInstallDarwinFindsBundle(t *testing.T) {
	base := t.TempDir()
	bundle := filepath.Join(base, "DemucsStudio.app")
	exe := filepath.Join(bundle, "Contents", "MacOS", "DemucsStudio")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	got := detectInstallFor("darwin", exe)
	if got.kind != kindBundle {
		t.Fatalf("kind = %v, want kindBundle", got.kind)
	}
	if got.root != bundle {
		t.Errorf("root = %q, want %q", got.root, bundle)
	}
	if got.parent != base {
		t.Errorf("parent = %q, want %q", got.parent, base)
	}

	// A loose binary outside any bundle is not something we know how to swap.
	loose := filepath.Join(base, "DemucsStudio")
	if k := detectInstallFor("darwin", loose).kind; k != kindUnknown {
		t.Errorf("loose darwin binary kind = %v, want kindUnknown", k)
	}
}

func TestDetectInstallWindowsSwapsTheExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "demucs-studio.exe")
	got := detectInstallFor("windows", exe)
	if got.kind != kindPortable {
		t.Fatalf("kind = %v, want kindPortable", got.kind)
	}
	// Windows cannot rename a directory containing a running image, so the
	// swap target has to be the file itself.
	if got.root != exe {
		t.Errorf("root = %q, want the executable %q", got.root, exe)
	}
}

func TestSafeJoinBlocksTraversal(t *testing.T) {
	root := "/tmp/root"
	for _, name := range []string{"../evil", "../../etc/passwd", "/etc/passwd", "a/../../b"} {
		if _, err := safeJoin(root, name); err == nil {
			t.Errorf("safeJoin(%q) allowed, want rejected", name)
		}
	}
	got, err := safeJoin(root, "demucs-studio/run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "demucs-studio", "run.sh"); got != want {
		t.Errorf("safeJoin = %q, want %q", got, want)
	}
}

func TestSwapRollsBackWhenTheNewTreeCannotMoveIn(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "keep")
	if err := os.WriteFile(marker, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A staged path that does not exist makes the second rename fail, which is
	// the branch that must restore the original install.
	err := swap(root, filepath.Join(base, "missing"), discardReporter{})
	if err == nil {
		t.Fatal("expected swap to fail")
	}
	got, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("original install was not restored: %v", readErr)
	}
	if string(got) != "original" {
		t.Errorf("restored content = %q, want %q", got, "original")
	}
}

func TestSwapKeepsTheOldTreeAside(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	staged := filepath.Join(base, "staged")
	for _, d := range []string{root, staged} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "v"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "v"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := swap(root, staged, discardReporter{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "v"))
	if err != nil || string(got) != "new" {
		t.Fatalf("after swap root holds %q (err %v), want %q", got, err, "new")
	}
	// The old copy has to survive: the running process is still executing from
	// it, and CleanupOld removes it on the next start.
	old, err := os.ReadFile(filepath.Join(root+".old", "v"))
	if err != nil || string(old) != "old" {
		t.Fatalf("old copy = %q (err %v), want %q kept aside", old, err, "old")
	}
}

type discardReporter struct{}

func (discardReporter) Log(string, string)                   {}
func (discardReporter) Logf(string, string, ...any)          {}
func (discardReporter) Step(string, float64, string, string) {}
