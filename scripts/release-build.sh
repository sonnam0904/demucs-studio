#!/usr/bin/env bash
# Builds the Linux and Windows distributables for one version into dist/.
#
#   scripts/release-build.sh 1.4.0
#
# Called by semantic-release (prepareCmd in .releaserc.json) and by the build
# workflow. Fails if any expected artifact is missing — the `deb` target in the
# Makefile exits 0 when nfpm is absent, so without this check a release would
# quietly ship without .deb/.rpm.
#
# macOS cannot be cross-compiled from Linux, so it is built separately by
# scripts/release-build-macos.sh on a macOS runner and dropped into dist/ before
# this script runs. Set EXPECT_MACOS=1 to require that .dmg to be present.
set -euo pipefail

version="${1:?usage: release-build.sh <version>}"

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Deliberately version-agnostic: an archive left behind by an earlier version
# would otherwise survive here and be picked up by the release asset globs,
# publishing stale packages alongside the new ones. Only the archives this
# script produces are removed; anything else in dist/ — notably the .dmg the
# macOS job just uploaded — is left alone.
stale=('*-linux-amd64.tar.gz' '*.deb' '*.rpm' '*-windows-amd64.zip')
for pattern in "${stale[@]}"; do
	find dist -maxdepth 1 -type f -name "$pattern" -print -delete 2> /dev/null || true
done

# wails.json has no CLI override for productVersion, and Wails copies that value
# into the Windows .exe file properties — the -X ldflag alone leaves it stale.
tmp="$(mktemp)"
jq --arg v "$version" '.info.productVersion = $v' wails.json > "$tmp"
mv "$tmp" wails.json

# `wails build` generates the TypeScript bindings by compiling and running the
# app, so a headless runner needs a virtual display.
build=(make package "VERSION=$version")
if [ -z "${DISPLAY:-}" ] && command -v xvfb-run > /dev/null 2>&1; then
	build=(xvfb-run -a "${build[@]}")
fi
"${build[@]}"

# Unlike the cleanup globs these carry the version, so an artifact stamped with
# the wrong one fails here instead of being published under this release. That
# matters most for the .dmg: it is built by a separate job from a version
# computed in an earlier one, and this is what catches the two disagreeing.
expected=(
	"*-$version-linux-amd64.tar.gz"
	"*$version*.deb"
	"*$version*.rpm"
	"*-$version-windows-amd64.zip"
)
if [ "${EXPECT_MACOS:-}" = "1" ]; then
	expected+=("*-$version-macos-universal.dmg")
fi

missing=()
for pattern in "${expected[@]}"; do
	compgen -G "dist/$pattern" > /dev/null || missing+=("$pattern")
done
if [ ${#missing[@]} -gt 0 ]; then
	echo "release-build: no artifact matched: ${missing[*]}" >&2
	exit 1
fi

echo
echo "Artifacts for $version:"
ls -1 dist
