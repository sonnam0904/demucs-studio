#!/usr/bin/env sh
# Launcher for the portable tar.gz build.
#
# Two things it handles that a bare ./demucs-studio does not:
#   - running from wherever the archive was extracted, so a double-click from a
#     file manager finds the binary;
#   - a readable message when webkit2gtk is missing, instead of the linker's.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
bin="$here/demucs-studio"

if [ ! -x "$bin" ]; then
	echo "Không tìm thấy $bin" >&2
	exit 1
fi

if ! ldd "$bin" 2>/dev/null | grep -q 'libwebkit2gtk.*not found'; then
	exec "$bin" "$@"
fi

cat >&2 <<'EOF'
Thiếu WebKitGTK — không mở được cửa sổ ứng dụng.

  Debian/Ubuntu/Pop!_OS : sudo apt install libwebkit2gtk-4.1-0
  Fedora                : sudo dnf install webkit2gtk4.1
  Arch                  : sudo pacman -S webkit2gtk-4.1
EOF
exit 1
