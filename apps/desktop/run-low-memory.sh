#!/usr/bin/env bash
set -euo pipefail

desktop_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

"$desktop_dir/build-sidecar.sh"

cd "$desktop_dir"
env \
  CARGO_BUILD_JOBS=1 \
  CARGO_INCREMENTAL=0 \
  npm run tauri -- dev
