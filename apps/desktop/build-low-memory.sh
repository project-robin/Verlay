#!/usr/bin/env bash
set -euo pipefail

desktop_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
appimage_tmp="$desktop_dir/src-tauri/target/appimage-tmp"

"$desktop_dir/build-sidecar.sh"
mkdir -p "$appimage_tmp"

cd "$desktop_dir"
env \
  CARGO_BUILD_JOBS=1 \
  CARGO_INCREMENTAL=0 \
  NO_STRIP=1 \
  TMPDIR="$appimage_tmp" \
  npm run tauri -- build --bundles appimage
