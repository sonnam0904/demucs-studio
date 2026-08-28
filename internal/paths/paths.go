// Package paths resolves every on-disk location the app uses, so that the
// Linux and Windows packages can stay relocatable: nothing is compiled in as an
// absolute path, and a portable install (tools sitting next to the executable)
// is always preferred over a machine-wide one.
package paths

import (
	"os"
	"path/filepath"
	"runtime"
)

const appFolder = "DemucsStudio"

// ExeDir is the directory containing the running executable. Portable installs
// keep bundled yt-dlp/ffmpeg binaries in ExeDir/bin.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Dir(exe)
}

// AppDir is the writable per-user directory holding settings, downloaded
// helper binaries, models and the managed Python environment.
func AppDir() string {
	if custom := os.Getenv("DEMUCS_STUDIO_HOME"); custom != "" {
		return custom
	}
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, appFolder)
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", appFolder)
		}
	default:
		if base := os.Getenv("XDG_DATA_HOME"); base != "" {
			return filepath.Join(base, "demucs-studio")
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".local", "share", "demucs-studio")
		}
	}
	return filepath.Join(".", appFolder)
}

func SettingsFile() string { return filepath.Join(AppDir(), "settings.json") }

// BinDir holds helper executables the app downloaded itself (yt-dlp, ffmpeg).
func BinDir() string { return filepath.Join(AppDir(), "bin") }

// BundledBinDir holds helper executables shipped alongside the app.
func BundledBinDir() string { return filepath.Join(ExeDir(), "bin") }

func ModelsDir() string { return filepath.Join(AppDir(), "models") }

// DemucsRepoDir is a demucs "--repo" folder: a flat directory of .th weights
// plus the bag-of-models YAML files that reference them.
func DemucsRepoDir() string { return filepath.Join(ModelsDir(), "demucs") }

// RoformerModelDir is the audio-separator "--model_file_dir".
func RoformerModelDir() string { return filepath.Join(ModelsDir(), "roformer") }

// VenvDir is the managed Python virtual environment used when the app installs
// demucs / audio-separator on the user's behalf.
func VenvDir() string { return filepath.Join(AppDir(), "pyenv") }

// VenvBin returns the path of an executable inside the managed venv.
func VenvBin(name string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(VenvDir(), "Scripts", name+".exe")
	}
	return filepath.Join(VenvDir(), "bin", name)
}

// TorchHubCache is where torch.hub caches downloaded checkpoints. Reusing it
// lets a machine that already ran demucs skip a 330 MB download.
func TorchHubCache() string {
	if v := os.Getenv("TORCH_HOME"); v != "" {
		return filepath.Join(v, "hub", "checkpoints")
	}
	if runtime.GOOS == "windows" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".cache", "torch", "hub", "checkpoints")
		}
	}
	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return filepath.Join(base, "torch", "hub", "checkpoints")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".cache", "torch", "hub", "checkpoints")
	}
	return ""
}

// DefaultOutputDir is where downloads and stems land unless the user picks
// somewhere else.
func DefaultOutputDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(AppDir(), "output")
	}
	if music := filepath.Join(home, "Music"); isDir(music) {
		return filepath.Join(music, appFolder)
	}
	return filepath.Join(home, appFolder)
}

// Exe appends the platform's executable suffix.
func Exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func EnsureDir(dir string) error { return os.MkdirAll(dir, 0o755) }

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
