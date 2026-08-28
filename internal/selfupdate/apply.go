package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"demucs-studio/internal/netfetch"
)

// Log levels, matching internal/bus so the lines render like every other job.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// Reporter is the subset of the event bus this package needs.
type Reporter interface {
	Log(level, text string)
	Logf(level, format string, args ...any)
	Step(phase string, fraction float64, label, detail string)
}

// Apply downloads the release package in st and swaps it over this install. It
// does not restart anything — the caller decides when to hand over, by calling
// Relaunch and then quitting.
//
// The swap is a rename, never a copy over the live files, so an interrupted
// update leaves either the old install or the new one, never a half-written
// mixture. The previous copy is kept alongside as "<root>.old" and removed by
// CleanupOld on the next start; it is what makes the rollback below possible
// and what lets the running process keep executing from its now-renamed image.
func Apply(ctx context.Context, st Status, rep Reporter) error {
	return applyTo(ctx, st, detectInstall(), rep)
}

// applyTo is Apply against an explicit install, so the download-and-swap path
// can be exercised against a throwaway copy instead of the running one.
func applyTo(ctx context.Context, st Status, inst install, rep Reporter) error {
	if !st.Available || st.AssetURL == "" {
		return errors.New("không có bản cập nhật để cài")
	}
	if ok, reason := inst.updatable(); !ok {
		return errors.New(reason)
	}

	// Staged inside the parent directory so the swap below is a rename within
	// one filesystem. A temp dir under /tmp is routinely a different mount, and
	// os.Rename cannot cross one.
	stageDir, err := os.MkdirTemp(inst.parent, ".demucs-studio-update-")
	if err != nil {
		return fmt.Errorf("tạo thư mục tạm: %w", err)
	}
	defer os.RemoveAll(stageDir)

	rep.Logf(LevelInfo, "Tải bản %s: %s", st.Latest, st.AssetURL)
	archive := filepath.Join(stageDir, st.AssetName)
	err = netfetch.Get(ctx, st.AssetURL, archive, "", func(done, total int64) {
		rep.Step("update", fraction(done, total)*0.9, "Đang tải bản cập nhật", byteRange(done, total))
	})
	if err != nil {
		return fmt.Errorf("tải bản cập nhật: %w", err)
	}

	rep.Step("update", 0.92, "Đang giải nén", "")
	staged, err := stage(ctx, inst, archive, stageDir)
	if err != nil {
		return err
	}

	rep.Step("update", 0.97, "Đang thay thế bản cũ", "")
	if err := swap(inst.root, staged, rep); err != nil {
		return err
	}
	rep.Step("update", 1, "Đã cập nhật", "khởi động lại để dùng bản mới")
	rep.Logf(LevelInfo, "Đã cập nhật lên %s.", st.Latest)
	return nil
}

// stage unpacks the downloaded archive and returns the path of the tree that is
// ready to be renamed over inst.root. What that is differs per platform: the
// whole extracted directory on Linux, the single executable on Windows, the
// .app copied off the mounted disk image on macOS.
func stage(ctx context.Context, inst install, archive, stageDir string) (string, error) {
	dest := filepath.Join(stageDir, "new")
	switch runtime.GOOS {
	case "linux":
		if err := untarGz(archive, dest); err != nil {
			return "", fmt.Errorf("giải nén .tar.gz: %w", err)
		}
		// The archive holds one top-level directory (demucs-studio/); that
		// directory, not its container, is what replaces the install root.
		inner, err := soleDir(dest)
		if err != nil {
			return "", err
		}
		if err := verifyExecutable(filepath.Join(inner, filepath.Base(inst.exe))); err != nil {
			return "", err
		}
		return inner, nil

	case "windows":
		// inst.root is the .exe itself, so only the executable is extracted.
		exeName := filepath.Base(inst.exe)
		out := filepath.Join(dest, exeName)
		if err := unzipOne(archive, exeName, out); err != nil {
			return "", fmt.Errorf("giải nén .zip: %w", err)
		}
		if err := verifyExecutable(out); err != nil {
			return "", err
		}
		return out, nil

	case "darwin":
		app, err := copyAppFromDMG(ctx, archive, dest)
		if err != nil {
			return "", err
		}
		if err := verifyExecutable(filepath.Join(app, "Contents", "MacOS", filepath.Base(inst.exe))); err != nil {
			return "", err
		}
		return app, nil
	}
	return "", fmt.Errorf("không hỗ trợ tự cập nhật trên %s", runtime.GOOS)
}

// swap renames the new tree into place, keeping the old one next to it.
func swap(root, staged string, rep Reporter) error {
	old := root + ".old"
	if err := os.RemoveAll(old); err != nil {
		// Almost always the previous update's copy still being executed by this
		// very process on Windows, which refuses to delete a running image. A
		// unique name sidesteps it; CleanupOld globs for the prefix.
		old = fmt.Sprintf("%s.old.%d", root, os.Getpid())
		_ = os.RemoveAll(old)
	}
	if err := os.Rename(root, old); err != nil {
		return fmt.Errorf("không đổi tên được bản cũ: %w", err)
	}
	if err := os.Rename(staged, root); err != nil {
		// Put the working copy back rather than leaving the user with nothing
		// to launch.
		if back := os.Rename(old, root); back != nil {
			rep.Logf(LevelError, "Cập nhật hỏng và không khôi phục được bản cũ. Bản cũ đang ở %s", old)
			return fmt.Errorf("cài bản mới thất bại (%v) và khôi phục cũng thất bại: %w", err, back)
		}
		return fmt.Errorf("cài bản mới thất bại, đã khôi phục bản cũ: %w", err)
	}
	return nil
}

// Relaunch starts the freshly installed copy as a detached process. The caller
// quits immediately after, so the two never share the window.
func Relaunch() error {
	inst := detectInstall()
	if inst.exe == "" {
		return errors.New("không xác định được đường dẫn ứng dụng")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" && inst.kind == kindBundle {
		// `open` launches through LaunchServices, which gives the new process
		// the bundle identity and activation the inner binary would not get if
		// exec'd directly. -n forces a new instance instead of reactivating the
		// one that is on its way out.
		cmd = exec.Command("open", "-n", inst.root)
	} else {
		cmd = exec.Command(inst.exe)
		cmd.Dir = filepath.Dir(inst.exe)
	}
	detach(cmd)
	return cmd.Start()
}

// CleanupOld removes what a previous update left behind. Called at startup,
// because that is the first moment the old image is no longer being executed.
func CleanupOld() {
	inst := detectInstall()
	if inst.root == "" {
		return
	}
	matches, err := filepath.Glob(inst.root + ".old*")
	if err != nil {
		return
	}
	for _, m := range matches {
		_ = os.RemoveAll(m)
	}
}

// verifyExecutable rejects an archive that did not contain the binary where it
// was expected, before anything irreversible happens. Without this the swap
// would happily install a tree with no executable in it.
func verifyExecutable(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("gói tải về thiếu %s", filepath.Base(path))
	}
	if st.IsDir() || st.Size() == 0 {
		return fmt.Errorf("%s trong gói tải về không hợp lệ", filepath.Base(path))
	}
	return nil
}

// soleDir returns the single directory inside dir, which is how both the
// .tar.gz and the .dmg are laid out.
func soleDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if found != "" {
			return "", errors.New("gói tải về có nhiều thư mục gốc, không rõ cái nào là ứng dụng")
		}
		found = filepath.Join(dir, e.Name())
	}
	if found == "" {
		return "", errors.New("gói tải về không có thư mục ứng dụng")
	}
	return found, nil
}

func untarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeJoin(dest, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// Preserve the executable bit the archive recorded; the launcher
			// and the binary are both useless without it.
			mode := os.FileMode(h.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			if err := writeFile(target, tr, mode); err != nil {
				return err
			}
		}
		// Symlinks and devices are skipped: the archive contains none, and
		// honouring them would reintroduce the traversal safeJoin prevents.
	}
}

// unzipOne extracts the single entry whose base name is want.
func unzipOne(archive, want, dest string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return writeFile(dest, rc, 0o755)
	}
	return fmt.Errorf("không tìm thấy %s trong gói .zip", want)
}

// copyAppFromDMG mounts the disk image, copies the bundle off it and unmounts
// again, returning the copy. ditto rather than a Go file walk: it is the only
// copy that reliably preserves bundle metadata, symlinks inside frameworks and
// the code signature — a bundle copied naively fails to launch.
func copyAppFromDMG(ctx context.Context, dmg, dest string) (string, error) {
	mount := dmg + ".mnt"
	if err := os.MkdirAll(mount, 0o755); err != nil {
		return "", err
	}
	attach := exec.CommandContext(ctx, "hdiutil", "attach", dmg,
		"-nobrowse", "-readonly", "-noverify", "-mountpoint", mount)
	if out, err := attach.CombinedOutput(); err != nil {
		return "", fmt.Errorf("gắn .dmg thất bại: %v: %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		// Best effort: a still-mounted image is untidy but harmless, and
		// failing the update over it would be worse.
		_ = exec.Command("hdiutil", "detach", mount, "-force").Run()
	}()

	app, err := soleDir(mount)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dest, filepath.Base(app))
	if o, err := exec.CommandContext(ctx, "ditto", app, out).CombinedOutput(); err != nil {
		return "", fmt.Errorf("sao chép .app thất bại: %v: %s", err, strings.TrimSpace(string(o)))
	}
	return out, nil
}

// safeJoin resolves name under root, refusing anything that would escape it.
// Release archives are ours, but an archive is attacker-controlled input the
// moment a download is redirected, and zip-slip costs one line to close.
func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("đường dẫn không hợp lệ trong gói: %q", name)
	}
	return filepath.Join(root, clean), nil
}

func writeFile(dest string, src io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fraction(done, total int64) float64 {
	if total <= 0 {
		return -1
	}
	return float64(done) / float64(total)
}

func byteRange(done, total int64) string {
	if total <= 0 {
		return humanBytes(done)
	}
	return humanBytes(done) + " / " + humanBytes(total)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PB", value)
}
