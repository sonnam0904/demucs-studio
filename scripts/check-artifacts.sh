#!/usr/bin/env bash
# Asserts that dist/ holds the release artifacts for a version.
#
#   scripts/check-artifacts.sh 1.4.0 linux windows macos
#
# Every producer calls this, so the expected filenames are declared in exactly
# one place. Two failures it is there to catch:
#
#   - the `deb` target in the Makefile exits 0 when nfpm is missing, so a build
#     can otherwise "succeed" while silently shipping no .deb/.rpm;
#   - the platform jobs run in parallel from a version computed in an earlier
#     job, so a file stamped with a different version would otherwise be
#     published under this release.
#
# The patterns carry the version for exactly that second reason.
set -euo pipefail

version="${1:?usage: check-artifacts.sh <version> <platform...>}"
shift
if [ "$#" -eq 0 ]; then
	echo "check-artifacts: cần ít nhất một platform (linux, windows, macos)" >&2
	exit 1
fi

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

expected=()
for platform in "$@"; do
	case "$platform" in
	linux)
		expected+=("*-$version-linux-amd64.tar.gz" "*$version*.deb" "*$version*.rpm")
		;;
	windows)
		expected+=("*-$version-windows-amd64.zip")
		;;
	macos)
		expected+=("*-$version-macos-universal.dmg")
		;;
	*)
		echo "check-artifacts: platform không hợp lệ: $platform" >&2
		exit 1
		;;
	esac
done

missing=()
for pattern in "${expected[@]}"; do
	compgen -G "dist/$pattern" > /dev/null || missing+=("$pattern")
done

if [ ${#missing[@]} -gt 0 ]; then
	# Redirect as a block: `ls … 2>/dev/null >&2` would point stdout at the
	# /dev/null that fd 2 had just been aimed at, silencing the listing exactly
	# when it is needed.
	{
		echo "check-artifacts: không có file nào khớp: ${missing[*]}"
		echo "dist/ đang có:"
		if [ -d dist ] && [ -n "$(ls -A dist 2> /dev/null)" ]; then
			ls -1 dist | sed 's/^/  /'
		else
			echo "  (trống hoặc chưa có thư mục dist/)"
		fi
	} >&2
	exit 1
fi

echo "check-artifacts: đủ artifact cho $version ($*)"
