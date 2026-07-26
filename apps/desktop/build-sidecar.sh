#!/usr/bin/env bash
set -euo pipefail

desktop_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="$(cd "$desktop_dir/../.." && pwd)"
output_dir="$desktop_dir/src-tauri/binaries"

mkdir -p "$output_dir"

cd "$repo_dir"
env \
  CGO_ENABLED=0 \
  GOOS=linux \
  GOARCH=amd64 \
  GOMODCACHE="${GOMODCACHE:-/tmp/croc-go-mod-cache}" \
  GOCACHE="${GOCACHE:-/tmp/croc-linux-go-cache}" \
  go build \
    -trimpath \
    -ldflags="-s -w" \
    -o "$output_dir/croc-x86_64-unknown-linux-gnu" \
    .

printf 'Built %s\n' "$output_dir/croc-x86_64-unknown-linux-gnu"
