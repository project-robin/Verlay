#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || pwd)"
repo_root="$(cd "$script_dir/.." 2>/dev/null && pwd || true)"
install_root="${MESH_INSTALL_ROOT:-$HOME/.local}"
binary_dir="$install_root/bin"
service_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
mesh_data_dir="${XDG_DATA_HOME:-$HOME/.local/share}/mesh"
start_service="${MESH_INSTALL_START_SERVICE:-1}"
release_repository="${VERLAY_REPOSITORY:-project-robin/Verlay}"
release_version="${AGENT_MESH_VERSION:-latest}"
binary="$binary_dir/mesh"
service="$service_dir/mesh.service"

if [[ "${1:-}" == "--uninstall" ]]; then
  systemctl --user disable --now mesh.service 2>/dev/null || true
  rm -f "$service" "$binary"
  systemctl --user daemon-reload
  printf 'Removed mesh binary and user service. Device identity and trust data were preserved.\n'
  exit 0
fi

mkdir -p "$binary_dir" "$service_dir" "$mesh_data_dir" "$HOME/Downloads/Mesh"
chmod 0700 "$mesh_data_dir" "$HOME/Downloads/Mesh"
build_output="$(mktemp "${TMPDIR:-/tmp}/mesh-build.XXXXXX")"
checksum_output="$(mktemp "${TMPDIR:-/tmp}/mesh-checksums.XXXXXX")"
trap 'rm -f "$build_output" "$checksum_output"' EXIT

if [[ -f "$repo_root/go.mod" && -d "$repo_root/cmd/mesh" ]]; then
  (
    cd "$repo_root"
    go build -trimpath -ldflags "-s -w" -o "$build_output" ./cmd/mesh
  )
else
  case "$(uname -m)" in
    x86_64|amd64) asset="mesh-linux-amd64" ;;
    aarch64|arm64) asset="mesh-linux-arm64" ;;
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

if [[ "$install_root" == "$HOME/.local" && -f "$repo_root/packaging/linux/mesh.service" ]]; then
  install -m 0644 "$repo_root/packaging/linux/mesh.service" "$service"
else
  service_binary="$binary"
  if [[ "$install_root" == "$HOME/.local" ]]; then
    service_binary="%h/.local/bin/mesh"
  fi
  {
    printf '%s\n' \
      '[Unit]' \
      'Description=Verlay secure device daemon' \
      'After=network-online.target' \
      'Wants=network-online.target' \
      '' \
      '[Service]' \
      'Type=simple' \
      "ExecStart=$service_binary daemon run" \
      'Restart=on-failure' \
      'RestartSec=3' \
      'UMask=0077' \
      'NoNewPrivileges=true' \
      'PrivateTmp=true' \
      'ProtectSystem=strict' \
      'ProtectHome=read-only' \
      'ConfigurationDirectory=mesh' \
      'ConfigurationDirectoryMode=0700' \
      'StateDirectory=mesh' \
      'StateDirectoryMode=0700' \
      'RuntimeDirectory=mesh' \
      'RuntimeDirectoryMode=0700' \
      'ReadWritePaths=%h/.local/share/mesh %h/Downloads/Mesh' \
      '' \
      '[Install]' \
      'WantedBy=default.target' > "$service"
  }
  chmod 0644 "$service"
fi

if [[ "$start_service" == "1" ]]; then
  systemctl --user daemon-reload
  systemctl --user enable mesh.service
  systemctl --user restart mesh.service
  for _ in {1..50}; do
    if "$binary" --json status >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  "$binary" --json status
  printf 'Installed mesh at %s and started mesh.service.\n' "$binary"
else
  printf 'Installed mesh at %s without starting the user service.\n' "$binary"
fi
