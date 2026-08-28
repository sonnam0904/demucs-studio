#!/usr/bin/env bash
# Writes a version into wails.json.
#
#   scripts/stamp-version.sh 1.4.0
#
# wails.json has no CLI override for productVersion, and Wails copies that value
# into the Windows .exe file properties and into the macOS bundle's Info.plist —
# the -X ldflag alone leaves both stale. Each platform build stamps it before
# building, and semantic-release stamps it once more in the release job so the
# value committed back to the repo matches what actually shipped.
set -euo pipefail

version="${1:?usage: stamp-version.sh <version>}"

cd "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

tmp="$(mktemp)"
jq --arg v "$version" '.info.productVersion = $v' wails.json > "$tmp"
mv "$tmp" wails.json
