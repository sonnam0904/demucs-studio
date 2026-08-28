package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeTarGz builds an archive laid out the way `make package-linux` does:
// one top-level demucs-studio/ directory holding the binary and its helpers.
func writeTarGz(t *testing.T, path string, files map[string]string, mode int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestStageUnpacksThePortableArchive(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the .tar.gz path is the Linux one")
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "demucs-studio-1.2.0-linux-amd64.tar.gz")
	writeTarGz(t, archive, map[string]string{
		"demucs-studio/demucs-studio": "ELF",
		"demucs-studio/run.sh":        "#!/bin/sh",
		"demucs-studio/README.md":     "docs",
	}, 0o755)

	inst := install{kind: kindPortable, exe: "/somewhere/demucs-studio/demucs-studio"}
	got, err := stage(context.Background(), inst, archive, dir)
	if err != nil {
		t.Fatal(err)
	}
	// stage must return the inner directory, not its container: that inner one
	// is what gets renamed over the install root.
	if filepath.Base(got) != "demucs-studio" {
		t.Errorf("staged path = %q, want the demucs-studio/ directory", got)
	}
	for _, name := range []string{"demucs-studio", "run.sh", "README.md"} {
		if _, err := os.Stat(filepath.Join(got, name)); err != nil {
			t.Errorf("%s missing from staged tree: %v", name, err)
		}
	}
	// The executable bit has to survive the round trip, or the relaunched app
	// cannot start.
	st, err := os.Stat(filepath.Join(got, "demucs-studio"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("binary lost its executable bit: mode %v", st.Mode().Perm())
	}
}

func TestStageRejectsAnArchiveMissingTheBinary(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the .tar.gz path is the Linux one")
	}
	dir := t.TempDir()
	archive := filepath.Join(dir, "bad.tar.gz")
	// Everything but the executable. Without the check in stage this would be
	// swapped in and leave the user with nothing to launch.
	writeTarGz(t, archive, map[string]string{
		"demucs-studio/run.sh": "#!/bin/sh",
	}, 0o755)

	inst := install{kind: kindPortable, exe: "/somewhere/demucs-studio/demucs-studio"}
	if _, err := stage(context.Background(), inst, archive, dir); err == nil {
		t.Fatal("stage accepted an archive with no binary in it")
	}
}

func TestUntarGzRefusesToEscapeTheDestination(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	writeTarGz(t, archive, map[string]string{"../escaped": "pwned"}, 0o644)

	if err := untarGz(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("untarGz followed a path outside the destination")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped")); err == nil {
		t.Fatal("a file was written outside the destination directory")
	}
}
