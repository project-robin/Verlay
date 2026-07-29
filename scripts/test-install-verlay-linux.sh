#!/usr/bin/env bash
set -euo pipefail

fake_systemctl() {
  [[ "${1:-}" == "--user" ]] && shift
  local command="${1:-}" service="" pid_file="" pid=""
  shift || true
  while [[ "${1:-}" == --* ]]; do
    [[ "$1" == "--now" ]] && stop_now=1
    shift
  done
  service="${1:-}"
  pid_file="$VERLAY_TEST_SYSTEMD_ROOT/$service.pid"
  case "$command" in
    daemon-reload)
      return 0
      ;;
    enable)
      : >"$VERLAY_TEST_SYSTEMD_ROOT/$service.enabled"
      ;;
    disable)
      rm -f "$VERLAY_TEST_SYSTEMD_ROOT/$service.enabled"
      if [[ "${stop_now:-0}" == "1" ]]; then
        fake_systemctl --user stop "$service"
      fi
      ;;
    is-active)
      [[ -f "$pid_file" ]] || return 3
      pid="$(<"$pid_file")"
      kill -0 "$pid" 2>/dev/null
      ;;
    start|restart)
      fake_systemctl --user stop "$service" 2>/dev/null || true
      if [[ "$service" == "verlayd.service" &&
            "${VERLAY_TEST_FAIL_VERLAYD:-0}" == "1" ]]; then
        return 0
      fi
      if [[ "$service" == "mesh.service" ]]; then
        "$HOME/.local/bin/mesh" daemon run \
          >"$VERLAY_TEST_SYSTEMD_ROOT/mesh.log" 2>&1 &
      else
        "$HOME/.local/bin/verlayd" \
          >"$VERLAY_TEST_SYSTEMD_ROOT/verlayd.log" 2>&1 &
      fi
      printf '%s\n' "$!" >"$pid_file"
      ;;
    stop)
      if [[ -f "$pid_file" ]]; then
        pid="$(<"$pid_file")"
        kill "$pid" 2>/dev/null || true
        for _ in {1..50}; do
          kill -0 "$pid" 2>/dev/null || break
          sleep 0.02
        done
        rm -f "$pid_file"
      fi
      ;;
    *)
      printf 'fake systemctl does not support: %s\n' "$command" >&2
      return 2
      ;;
  esac
}

if [[ "${VERLAY_TEST_SYSTEMCTL_MODE:-0}" == "1" &&
      "$(basename "$0")" == "systemctl" ]]; then
  fake_systemctl "$@"
  exit $?
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d /tmp/verlay-install-test.XXXXXX)"
fixture="$test_root/verlay"
fake_bin="$test_root/fake-bin"

cleanup() {
  while IFS= read -r pid_file; do
    pid="$(<"$pid_file")"
    kill "$pid" 2>/dev/null || true
  done < <(find "$test_root" -name '*.pid' -type f 2>/dev/null)
}
trap cleanup EXIT

mkdir -p "$fake_bin"
ln -s "$repo_root/scripts/test-install-verlay-linux.sh" "$fake_bin/systemctl"
(
  cd "$repo_root"
  go build -o "$fixture" ./cmd/verlay
)

json_string() {
  local field="$1"
  sed -n 's/.*"'"$field"'":"\([^"]*\)".*/\1/p'
}

run_cli() {
  local root="$1" binary="$2"
  shift 2
  env \
    HOME="$root/home" \
    XDG_CONFIG_HOME="$root/home/.config" \
    XDG_DATA_HOME="$root/home/.local/share" \
    XDG_RUNTIME_DIR="$root/run" \
    "$binary" "$@"
}

wait_for_status() {
  local root="$1" binary="$2"
  for _ in {1..100}; do
    run_cli "$root" "$binary" --json status >/dev/null 2>&1 && return 0
    sleep 0.05
  done
  return 1
}

prepare_legacy() {
  local root="$1"
  mkdir -p "$root/home/.local/bin" "$root/home/.config/systemd/user" \
    "$root/home/.config/mesh" "$root/home/.local/share/mesh" \
    "$root/run" "$root/systemd"
  install -m 0755 "$fixture" "$root/home/.local/bin/mesh"
  printf '%s\n' \
    '[Service]' \
    "ExecStart=$root/home/.local/bin/mesh daemon run" \
    >"$root/home/.config/systemd/user/mesh.service"
  printf 'preserve me\n' >"$root/home/.config/mesh/legacy-marker"
  printf 'preserve me\n' >"$root/home/.local/share/mesh/legacy-marker"
}

run_systemctl() {
  local root="$1"
  shift
  env \
    HOME="$root/home" \
    XDG_CONFIG_HOME="$root/home/.config" \
    XDG_DATA_HOME="$root/home/.local/share" \
    XDG_RUNTIME_DIR="$root/run" \
    VERLAY_TEST_SYSTEMD_ROOT="$root/systemd" \
    VERLAY_TEST_SYSTEMCTL_MODE=1 \
    PATH="$fake_bin:$PATH" \
    systemctl --user "$@"
}

run_installer() {
  local root="$1"
  shift
  env \
    HOME="$root/home" \
    XDG_CONFIG_HOME="$root/home/.config" \
    XDG_DATA_HOME="$root/home/.local/share" \
    XDG_RUNTIME_DIR="$root/run" \
    VERLAY_INSTALL_ROOT="$root/home/.local" \
    VERLAY_INSTALL_SOURCE=checkout \
    VERLAY_TEST_SYSTEMD_ROOT="$root/systemd" \
    VERLAY_TEST_SYSTEMCTL_MODE=1 \
    PATH="$fake_bin:$PATH" \
    "$repo_root/scripts/install-verlay-linux.sh" "$@"
}

printf '1/3 preserve legacy state during migration\n'
preserve_root="$test_root/preserve"
prepare_legacy "$preserve_root"
run_systemctl "$preserve_root" start mesh.service
wait_for_status "$preserve_root" "$preserve_root/home/.local/bin/mesh"
old_id="$(run_cli "$preserve_root" "$preserve_root/home/.local/bin/mesh" --json status | json_string deviceId)"
run_installer "$preserve_root" >/dev/null
new_id="$(run_cli "$preserve_root" "$preserve_root/home/.local/bin/verlay" --json status | json_string deviceId)"
[[ -n "$old_id" && "$new_id" == "$old_id" ]]
[[ -f "$preserve_root/home/.config/mesh/legacy-marker" ]]
[[ -f "$preserve_root/home/.local/share/mesh/legacy-marker" ]]
[[ ! -e "$preserve_root/home/.local/bin/mesh" ]]
[[ ! -e "$preserve_root/home/.config/systemd/user/mesh.service" ]]
(
  cd /tmp
  PATH="$preserve_root/home/.local/bin:$PATH" command -v verlay >/dev/null
  PATH="$preserve_root/home/.local/bin:$PATH" verlay --json version >/dev/null
)

printf '2/3 explicit reset creates a clean identity\n'
reset_root="$test_root/reset"
prepare_legacy "$reset_root"
run_systemctl "$reset_root" start mesh.service
wait_for_status "$reset_root" "$reset_root/home/.local/bin/mesh"
old_id="$(run_cli "$reset_root" "$reset_root/home/.local/bin/mesh" --json status | json_string deviceId)"
run_installer "$reset_root" --reset-legacy >/dev/null
new_id="$(run_cli "$reset_root" "$reset_root/home/.local/bin/verlay" --json status | json_string deviceId)"
[[ -n "$new_id" && "$new_id" != "$old_id" ]]
[[ ! -e "$reset_root/home/.config/mesh/legacy-marker" ]]
[[ ! -e "$reset_root/home/.local/share/mesh/legacy-marker" ]]
[[ ! -e "$reset_root/home/.local/bin/mesh" ]]

printf '3/3 failed migration restores the legacy daemon\n'
rollback_root="$test_root/rollback"
prepare_legacy "$rollback_root"
run_systemctl "$rollback_root" start mesh.service
wait_for_status "$rollback_root" "$rollback_root/home/.local/bin/mesh"
if VERLAY_TEST_FAIL_VERLAYD=1 run_installer "$rollback_root" >/dev/null 2>&1; then
  printf 'expected migration failure\n' >&2
  exit 1
fi
run_systemctl "$rollback_root" is-active --quiet mesh.service
wait_for_status "$rollback_root" "$rollback_root/home/.local/bin/mesh"
[[ -e "$rollback_root/home/.local/bin/mesh" ]]
[[ -e "$rollback_root/home/.config/systemd/user/mesh.service" ]]

printf 'PASS: global install, legacy preservation, reset, and rollback\n'
cleanup
rm -rf -- "$test_root"
trap - EXIT
