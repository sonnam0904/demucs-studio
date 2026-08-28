package selfupdate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestApplyDownloadsAndSwapsForReal exercises the whole update path against the
// live GitHub release: download, unpack, verify, swap, keep the old copy aside.
// Only the relaunch is left out, because it would start a window.
//
// Gated like pipeline_test.go, since it reaches the network and pulls several
// megabytes:
//
//	DEMUCS_STUDIO_E2E=1 go test ./internal/selfupdate/ -run TestApplyDownloads -v
//
// It never touches the real install — everything happens inside t.TempDir().
func TestApplyDownloadsAndSwapsForReal(t *testing.T) {
	if os.Getenv("DEMUCS_STUDIO_E2E") != "1" {
		t.Skip("đặt DEMUCS_STUDIO_E2E=1 để chạy (tải thật từ GitHub)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("bản dựng test này chỉ mô phỏng layout portable của Linux")
	}

	// A stand-in for the .tar.gz layout: a directory holding the binary and
	// run.sh, which is what detectInstall keys on.
	parent := t.TempDir()
	root := filepath.Join(parent, "demucs-studio")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "demucs-studio")
	for _, f := range []string{exe, filepath.Join(root, "run.sh")} {
		if err := os.WriteFile(f, []byte("stale"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	inst := install{kind: kindPortable, exe: exe, root: root, parent: parent}

	// An old version, so the live release always counts as newer.
	st, err := Check(context.Background(), "0.0.1")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !st.Available {
		t.Skipf("chưa có bản nào mới hơn 0.0.1 (latest=%q)", st.Latest)
	}
	t.Logf("cài %s từ %s (%d bytes)", st.Latest, st.AssetName, st.AssetSize)

	if err := applyTo(context.Background(), st, inst, testReporter{t}); err != nil {
		t.Fatalf("applyTo: %v", err)
	}

	// The binary must have been replaced by a real one, not left as the stub.
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("binary missing after update: %v", err)
	}
	if info.Size() < 1<<20 {
		t.Errorf("binary is %d bytes, expected a real build", info.Size())
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("binary is not executable: %v", info.Mode().Perm())
	}
	head, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) < 4 || string(head[:4]) != "\x7fELF" {
		t.Errorf("replacement is not an ELF binary")
	}

	// run.sh ships in the archive too, so the whole tree was swapped rather
	// than just the executable.
	if _, err := os.Stat(filepath.Join(root, "run.sh")); err != nil {
		t.Errorf("run.sh missing from the new tree: %v", err)
	}

	// The old copy has to remain: on a real update the process is still
	// executing from it.
	old, err := os.ReadFile(filepath.Join(root+".old", "demucs-studio"))
	if err != nil {
		t.Fatalf("previous install was not kept aside: %v", err)
	}
	if string(old) != "stale" {
		t.Errorf("kept-aside copy = %q, want the original %q", old, "stale")
	}

	// Nothing may be left in the parent but the install and its backup: a
	// forgotten staging directory would accumulate on every update.
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("parent holds %v, want only the install and its .old copy", names)
	}
}

type testReporter struct{ t *testing.T }

func (r testReporter) Log(level, text string) { r.t.Logf("[%s] %s", level, text) }

func (r testReporter) Logf(level, format string, args ...any) {
	r.t.Logf("[%s] "+format, append([]any{level}, args...)...)
}

func (testReporter) Step(string, float64, string, string) {}
