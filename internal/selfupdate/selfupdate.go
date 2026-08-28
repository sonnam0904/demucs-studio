// Package selfupdate checks GitHub Releases for a newer build and, where the
// installation is one the app is able to overwrite, replaces itself with it.
//
// Not every install can be updated in place. The .deb and .rpm packages put the
// binary in /usr/bin owned by root, with the package manager tracking every
// file it installed; overwriting it from here would fail on permissions, and
// would desynchronise the manager's file list if it somehow succeeded. Those
// installs are detected and reported with CanApply false, so the UI can send
// the user to the release page instead of offering a button that cannot work.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"demucs-studio/internal/netfetch"
)

const (
	// Repo is the GitHub "owner/name" the releases are published under.
	Repo = "sonnam0904/demucs-studio"

	latestAPI = "https://api.github.com/repos/" + Repo + "/releases/latest"

	// ReleasesURL is what the UI opens when the update cannot be applied in
	// place.
	ReleasesURL = "https://github.com/" + Repo + "/releases/latest"

	// notesLimit keeps a pathological release body from being pushed into the
	// webview whole.
	notesLimit = 4000
)

// apiURL is where Check looks for the newest release. A variable rather than a
// constant so tests can point it at a stub server instead of reaching GitHub.
var apiURL = latestAPI

// Status is everything the UI needs to decide what to show. It is returned even
// when no update exists, so the version display has something to render.
type Status struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	Notes     string `json:"notes"`
	URL       string `json:"url"`

	// CanApply is false when this install cannot overwrite itself — a .deb or
	// .rpm, an unrecognised layout, or a directory the user cannot write.
	// Reason says which, in Vietnamese, for display.
	CanApply bool   `json:"canApply"`
	Reason   string `json:"reason"`

	// AssetURL is the package matching this platform, empty when the release
	// published none for it.
	AssetURL  string `json:"assetUrl"`
	AssetName string `json:"assetName"`
	AssetSize int64  `json:"assetSize"`
}

// ghRelease is the subset of GitHub's release JSON this package reads.
type ghRelease struct {
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks GitHub for the newest release and compares it against current.
//
// A network failure is returned as an error, but it is not worth interrupting
// the user over: the caller runs this at startup and only surfaces a successful
// result.
func Check(ctx context.Context, current string) (Status, error) {
	st := Status{Current: current, URL: ReleasesURL}

	// A build with no -X main.appVersion has nothing meaningful to compare, and
	// "updating" a working dev tree to a release would overwrite it.
	if _, ok := parseVersion(current); !ok {
		st.Reason = "bản dev — không kiểm tra cập nhật"
		return st, nil
	}

	raw, err := netfetch.Fetch(ctx, apiURL)
	if err != nil {
		return st, err
	}
	var rel ghRelease
	if err := json.Unmarshal(raw, &rel); err != nil {
		return st, fmt.Errorf("đọc thông tin release: %w", err)
	}
	if rel.Draft || rel.Prerelease {
		// /releases/latest already excludes both; treat it as "nothing to do"
		// rather than offering an unfinished build.
		return st, nil
	}

	st.Latest = strings.TrimPrefix(rel.TagName, "v")
	if rel.HTMLURL != "" {
		st.URL = rel.HTMLURL
	}
	if _, ok := parseVersion(st.Latest); !ok {
		return st, fmt.Errorf("tag release không phải dạng x.y.z: %q", rel.TagName)
	}
	if CompareVersions(st.Latest, current) <= 0 {
		return st, nil
	}
	st.Available = true
	st.Notes = truncate(rel.Body, notesLimit)

	suffix, err := assetSuffix(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		st.Reason = err.Error()
		return st, nil
	}
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, suffix) {
			st.AssetURL, st.AssetName, st.AssetSize = a.URL, a.Name, a.Size
			break
		}
	}
	if st.AssetURL == "" {
		st.Reason = "bản phát hành mới không có gói cho nền tảng này"
		return st, nil
	}

	inst := detectInstall()
	st.CanApply, st.Reason = inst.updatable()
	return st, nil
}

// assetSuffix identifies this platform's package among the release assets. The
// release publishes amd64 only for Linux and Windows, plus one universal build
// for macOS, so any other target has nothing to download.
func assetSuffix(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "-linux-amd64.tar.gz", nil
	case "windows/amd64":
		return "-windows-amd64.zip", nil
	case "darwin/amd64", "darwin/arm64":
		return "-macos-universal.dmg", nil
	}
	return "", fmt.Errorf("bản phát hành không có gói cho %s/%s", goos, goarch)
}

// kind is how this copy was installed, which decides both whether Apply can
// replace it and what exactly it has to replace.
type kind int

const (
	// kindUnknown is a layout this package does not recognise.
	kindUnknown kind = iota
	// kindPortable is the Linux .tar.gz or Windows .zip layout: a directory the
	// user extracted themselves.
	kindPortable
	// kindBundle is the macOS .app the .dmg installs.
	kindBundle
	// kindManaged is a .deb or .rpm install, owned by the package manager.
	kindManaged
)

// install locates what an in-place update would have to replace.
type install struct {
	kind kind
	exe  string
	// root is what gets swapped out: the extracted directory or the .app
	// bundle. On Windows it is the executable itself — Windows refuses to
	// rename a directory holding a running image, but it does allow renaming
	// the image, which is what makes the swap possible there at all.
	root string
	// parent must be writable, because the swap is a rename within it.
	parent string
}

// detectInstall works out the layout from the running executable's path.
func detectInstall() install {
	exe, err := os.Executable()
	if err != nil {
		return install{kind: kindUnknown}
	}
	// A .deb install is reached through /usr/bin; resolving symlinks first
	// keeps a symlinked launcher from being mistaken for the real layout.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return detectInstallFor(runtime.GOOS, exe)
}

// detectInstallFor is the parameterised half of detectInstall, so the
// portable-versus-package-managed decision can be tested from any host. Getting
// it wrong on Linux would mean trying to overwrite /usr/bin.
func detectInstallFor(goos, exe string) install {
	dir := filepath.Dir(exe)

	switch goos {
	case "darwin":
		if bundle := bundleRoot(exe); bundle != "" {
			return install{kind: kindBundle, exe: exe, root: bundle, parent: filepath.Dir(bundle)}
		}
	case "windows":
		// Only ever distributed as the portable .zip.
		return install{kind: kindPortable, exe: exe, root: exe, parent: dir}
	case "linux":
		// run.sh ships only inside the .tar.gz; the .deb and .rpm install the
		// bare binary to /usr/bin. Its presence next to the executable is what
		// separates a directory we may replace from one dpkg/rpm owns.
		if _, err := os.Stat(filepath.Join(dir, "run.sh")); err == nil {
			return install{kind: kindPortable, exe: exe, root: dir, parent: filepath.Dir(dir)}
		}
		// Only blame the package manager when the binary actually sits where
		// one would have put it. A bare binary somewhere else — a `go build`
		// output, an odd manual copy — is not a .deb, and saying so would send
		// the user looking for a package that does not exist.
		if underSystemPrefix(dir) {
			return install{kind: kindManaged, exe: exe}
		}
	}
	return install{kind: kindUnknown, exe: exe}
}

// underSystemPrefix reports whether dir is one of the places a Linux package
// manager installs into, as opposed to somewhere a user extracted an archive.
func underSystemPrefix(dir string) bool {
	dir = filepath.Clean(dir)
	for _, prefix := range []string{"/usr", "/opt", "/bin", "/sbin", "/snap", "/var/lib/flatpak"} {
		if dir == prefix || strings.HasPrefix(dir, prefix+"/") {
			return true
		}
	}
	return false
}

// updatable reports whether Apply can run here, and why not when it cannot.
func (i install) updatable() (bool, string) {
	switch i.kind {
	case kindManaged:
		return false, "bản cài bằng .deb/.rpm do trình quản lý gói sở hữu — hãy tải gói mới và cài lại"
	case kindUnknown:
		return false, "không nhận ra kiểu cài đặt nên không tự cập nhật được"
	}
	if !writable(i.parent) {
		return false, "không có quyền ghi vào " + i.parent
	}
	return true, ""
}

// bundleRoot returns the .app directory holding exe, or "" when exe is not
// inside one. A macOS bundle always nests its binary at
// <name>.app/Contents/MacOS/<name>.
func bundleRoot(exe string) string {
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) == "MacOS" && filepath.Base(contents) == "Contents" &&
		strings.HasSuffix(app, ".app") {
		return app
	}
	return ""
}

// writable reports whether the current user can create a file in dir. Probing
// beats inspecting the mode bits: it also catches a read-only mount, an
// immutable flag, or an ACL that the permission bits do not describe.
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".dsupd-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// CompareVersions orders two "x.y.z" versions, tolerating a leading "v" and a
// semver prerelease suffix. It returns -1 when a sorts before b, 0 when they
// are equal, and 1 when a sorts after b. Unparseable input sorts lowest, so an
// unknown current version never looks newer than a real release.
func CompareVersions(a, b string) int {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	}
	for i := range av.num {
		if av.num[i] != bv.num[i] {
			if av.num[i] < bv.num[i] {
				return -1
			}
			return 1
		}
	}
	// Semver: 1.0.0-rc.1 precedes 1.0.0, so a release outranks any prerelease
	// of the same triple.
	switch {
	case av.pre == bv.pre:
		return 0
	case av.pre == "":
		return 1
	case bv.pre == "":
		return -1
	case av.pre < bv.pre:
		return -1
	}
	return 1
}

type version struct {
	num [3]int
	pre string
}

// parseVersion accepts "1.2.3", "v1.2.3" and "1.2.3-rc.1". Build metadata after
// "+" is ignored, as semver requires. Anything else — notably the "dev"
// default — fails, which is how callers detect a non-release build.
func parseVersion(s string) (version, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	if s == "" {
		return version{}, false
	}
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v version
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre, s = s[i+1:], s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.num[i] = n
	}
	return v, true
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
