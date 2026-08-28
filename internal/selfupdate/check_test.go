package selfupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

// realisticRelease mirrors the shape and the asset names GitHub actually serves
// for this project, so the matching below is tested against the real filenames
// rather than invented ones.
const realisticRelease = `{
  "tag_name": "v1.2.0",
  "body": "### Sửa lỗi\n- abc",
  "html_url": "https://github.com/sonnam0904/demucs-studio/releases/tag/v1.2.0",
  "draft": false,
  "prerelease": false,
  "assets": [
    {"name": "demucs-studio-1.2.0-1.x86_64.rpm", "size": 4816828,
     "browser_download_url": "https://example.invalid/rpm"},
    {"name": "demucs-studio-1.2.0-linux-amd64.tar.gz", "size": 4766888,
     "browser_download_url": "https://example.invalid/targz"},
    {"name": "demucs-studio-1.2.0-macos-universal.dmg", "size": 9922493,
     "browser_download_url": "https://example.invalid/dmg"},
    {"name": "demucs-studio-1.2.0-windows-amd64.zip", "size": 5050974,
     "browser_download_url": "https://example.invalid/zip"},
    {"name": "demucs-studio_1.2.0_amd64.deb", "size": 4828668,
     "browser_download_url": "https://example.invalid/deb"}
  ]
}`

// stubAPI points Check at a server returning body, for the duration of the test.
func stubAPI(t *testing.T, body string, status int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	previous := apiURL
	apiURL = srv.URL
	t.Cleanup(func() { apiURL = previous })
}

func TestCheckFindsNewerReleaseAndItsAsset(t *testing.T) {
	stubAPI(t, realisticRelease, http.StatusOK)

	st, err := Check(context.Background(), "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available {
		t.Fatal("1.2.0 should be offered to a 1.1.0 install")
	}
	if st.Latest != "1.2.0" {
		t.Errorf("Latest = %q, want 1.2.0 (the v prefix must be stripped)", st.Latest)
	}
	if st.URL != "https://github.com/sonnam0904/demucs-studio/releases/tag/v1.2.0" {
		t.Errorf("URL = %q, want the release's own page", st.URL)
	}
	// The asset picked must be this platform's, never another's — offering the
	// .deb to a Windows user is the failure this guards.
	suffix, err := assetSuffix(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skipf("no asset published for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	if !strings.HasSuffix(st.AssetName, suffix) {
		t.Errorf("AssetName = %q, want something ending in %q", st.AssetName, suffix)
	}
	if st.AssetSize == 0 || st.AssetURL == "" {
		t.Errorf("asset incompletely populated: %+v", st)
	}
}

func TestCheckStaysQuietWhenCurrent(t *testing.T) {
	stubAPI(t, realisticRelease, http.StatusOK)

	for _, current := range []string{"1.2.0", "1.3.0"} {
		st, err := Check(context.Background(), current)
		if err != nil {
			t.Fatal(err)
		}
		if st.Available {
			t.Errorf("current=%s should not be offered 1.2.0", current)
		}
		// The version display still needs something to render.
		if st.Current != current {
			t.Errorf("Current = %q, want %q", st.Current, current)
		}
	}
}

func TestCheckSkipsTheNetworkForDevBuilds(t *testing.T) {
	// No stub: reaching the network here would be the bug. A dev tree has no
	// version to compare, and replacing it with a release would destroy work.
	st, err := Check(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	if st.Available {
		t.Error("a dev build must never be offered an update")
	}
	if st.Reason == "" {
		t.Error("the UI needs a reason to show instead of a version comparison")
	}
}

func TestCheckReportsHTTPFailure(t *testing.T) {
	stubAPI(t, `{"message":"Not Found"}`, http.StatusNotFound)

	if _, err := Check(context.Background(), "1.0.0"); err == nil {
		t.Error("a 404 from the releases API should surface as an error")
	}
}

func TestCheckIgnoresDraftsAndPrereleases(t *testing.T) {
	stubAPI(t, `{"tag_name":"v9.9.9","draft":true,"prerelease":false,"assets":[]}`, http.StatusOK)

	st, err := Check(context.Background(), "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if st.Available {
		t.Error("an unfinished build must not be offered")
	}
}
