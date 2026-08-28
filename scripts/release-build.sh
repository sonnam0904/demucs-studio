#!/usr/bin/env bash
# Builds the Linux and Windows distributables for one version into dist/.
#
#   scripts/release-build.sh 1.4.0
#
# Windows cross-compiles from Linux because the Wails Windows backend uses a
# pure Go WebView2 loader, so one runner produces both. macOS cannot be
# cross-compiled and is built by scripts/release-build-macos.sh on a macOS
# runner, in parallel with this one; the release job collects both.
set -euo pipefail

version="${1:?usage: release-build.sh <version>}"

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Deliberately version-agnostic: an archive left behind by an earlier version
# would otherwise survive here and be picked up by the release asset globs,
# publishing stale packages alongside the new ones. Only the archives this
# script produces are removed; anything else in dist/ is left alone.
for pattern in '*-linux-amd64.tar.gz' '*.deb' '*.rpm' '*-windows-amd64.zip'; do
	find dist -maxdepth 1 -type f -name "$pattern" -print -delete 2> /dev/null || true
done

scripts/stamp-version.sh "$version"

# `wails build` generates the TypeScript bindings by compiling and running the
# app, so a headless runner needs a virtual display.
build=(make package "VERSION=$version")
if [ -z "${DISPLAY:-}" ] && command -v xvfb-run > /dev/null 2>&1; then
	build=(xvfb-run -a "${build[@]}")
fi
"${build[@]}"

scripts/check-artifacts.sh "$version" linux windows

echo
echo "Artifacts for $version:"
ls -1 dist
