#!/usr/bin/env bash
# Builds the macOS distributable for one version into dist/.
#
#   scripts/release-build-macos.sh 1.4.0
#
# The macOS counterpart to scripts/release-build.sh. It exists as a separate
# script because a Wails darwin build needs CGO and the system frameworks, so it
# only runs on a macOS host — the release workflow therefore builds it in its
# own job and hands the .dmg to the Linux job that publishes the release.
set -euo pipefail

version="${1:?usage: release-build-macos.sh <version>}"

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ "$(uname -s)" != "Darwin" ]; then
	echo "release-build-macos: needs a macOS host, got $(uname -s)" >&2
	exit 1
fi

# Version-agnostic on purpose, same reasoning as release-build.sh: a .dmg from
# an earlier version would otherwise survive and be uploaded alongside the new
# one.
find dist -maxdepth 1 -type f -name '*.dmg' -print -delete 2> /dev/null || true

# wails.json has no CLI override for productVersion, and Wails copies that value
# into the bundle's Info.plist (CFBundleVersion / CFBundleShortVersionString) —
# the -X ldflag alone leaves the version macOS shows in Finder stale.
tmp="$(mktemp)"
jq --arg v "$version" '.info.productVersion = $v' wails.json > "$tmp"
mv "$tmp" wails.json

make package-darwin "VERSION=$version"

dmg="dist/demucs-studio-$version-macos-universal.dmg"
if [ ! -f "$dmg" ]; then
	echo "release-build-macos: expected $dmg" >&2
	exit 1
fi

echo
echo "Artifacts for $version:"
ls -1 dist
