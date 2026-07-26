#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || pwd)"
repo_root="$(cd "$script_dir/.." 2>/dev/null && pwd || true)"
install_root="${VERLAY_INSTALL_ROOT:-$HOME/.local}"
binary_dir="$install_root/bin"
service_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mesh_data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/mesh"
start_service="${VERLAY_INSTALL_START_SERVICE:-1}"
release_repository="${VERLAY_REPOSITORY:-project-robin/Verlay}"
release_version="${VERLAY_VERSION:-latest}"
install_source="${VERLAY_INSTALL_SOURCE:-auto}"
binary="$binary_dir/verlay"
daemon_binary="$binary_dir/verlayd"
service="$service_dir/verlayd.service"

if [[ "${1:-}" == "--uninstall" ]]; then
  systemctl --user disable --now verlayd.service 2>/dev/null || true
  rm -f "$service" "$binary" "$daemon_binary"
  systemctl --user daemon-reload
  printf 'Removed Verlay binaries and user services. Device identity and trust data were preserved.\n'
  exit 0
fi

mkdir -p "$binary_dir" "$service_dir" "$mesh_data_dir" "$HOME/Downloads/Verlay"
chmod 0700 "$mesh_data_dir" "$HOME/Downloads/Verlay"
build_output="$(mktemp "${TMPDIR:-/tmp}/mesh-build.XXXXXX")"
checksum_output="$(mktemp "${TMPDIR:-/tmp}/mesh-checksums.XXXXXX")"
trap 'rm -f "$build_output" "$checksum_output"' EXIT

checkout_available=0
if [[ -f "$repo_root/go.mod" && -d "$repo_root/cmd/verlay" ]]; then
  checkout_available=1
fi

case "$install_source" in
  auto)
    if [[ "$checkout_available" == "1" ]] && command -v go >/dev/null; then
      build_from_checkout=1
    else
      build_from_checkout=0
    fi
    ;;
  checkout)
    [[ "$checkout_available" == "1" ]] || {
      printf 'A Verlay source checkout is required for VERLAY_INSTALL_SOURCE=checkout.\n' >&2
      exit 1
    }
    command -v go >/dev/null || {
      printf 'Go 1.25 or newer is required to build Verlay from source.\n' >&2
      exit 1
    }
    build_from_checkout=1
    ;;
  release)
    build_from_checkout=0
    ;;
  *)
    printf 'VERLAY_INSTALL_SOURCE must be auto, checkout, or release.\n' >&2
    exit 2
    ;;
esac

if [[ "$build_from_checkout" == "1" ]]; then
  (
    cd "$repo_root"
    go build -trimpath -ldflags "-s -w" -o "$build_output" ./cmd/verlay
  )
else
  case "$(uname -m)" in
    x86_64|amd64) asset="verlay-linux-amd64" ;;
    aarch64|arm64) asset="verlay-linux-arm64" ;;
    *)
      printf 'Unsupported Linux architecture: %s\n' "$(uname -m)" >&2
      exit 1
      ;;
  esac
  if [[ "$release_version" == "latest" ]]; then
    release_base="https://github.com/$release_repository/releases/latest/download"
  else
    release_base="https://github.com/$release_repository/releases/download/$release_version"
  fi
  command -v curl >/dev/null || {
    printf 'curl is required for release installation.\n' >&2
    exit 1
  }
  command -v sha256sum >/dev/null || {
    printf 'sha256sum is required for release verification.\n' >&2
    exit 1
  }
  curl --fail --location --silent --show-error "$release_base/$asset" --output "$build_output"
  curl --fail --location --silent --show-error "$release_base/verlay-checksums.txt" --output "$checksum_output"
  expected="$(awk -v wanted="$asset" '$2 == wanted {print $1}' "$checksum_output")"
  actual="$(sha256sum "$build_output" | awk '{print $1}')"
  if [[ -z "$expected" || "$actual" != "$expected" ]]; then
    printf 'Verlay checksum verification failed.\n' >&2
    exit 1
  fi
fi
install -m 0755 "$build_output" "$binary"
ln -sfn verlay "$daemon_binary"
install -m 0644 "$repo_root/packaging/linux/verlayd.service" "$service"
service_binary="${daemon_binary//&/\\&}"
service_binary="${service_binary//|/\\|}"
sed -i "s|^ExecStart=.*|ExecStart=$service_binary|" "$service"

if [[ "$start_service" == "1" ]]; then
  systemctl --user daemon-reload
  systemctl --user enable verlayd.service
  systemctl --user restart verlayd.service
  for _ in {1..50}; do
    if "$binary" --json status >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  "$binary" --json status
  printf 'Installed verlay at %s and started verlayd.service.\n' "$binary"
else
  printf 'Installed verlay and verlayd under %s without starting the user service.\n' "$binary_dir"
fi
