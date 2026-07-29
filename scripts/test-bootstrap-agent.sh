#!/usr/bin/env bash
set -euo pipefail

case "$(basename "$0")" in
  git)
    destination="${*: -1}"
    mkdir -p "$destination/scripts" "$destination/skills/verlay/references" \
      "$destination/packaging/linux"
    install -m 0644 "$VERLAY_TEST_REPO_ROOT/go.mod" "$destination/go.mod"
    install -m 0755 \
      "$VERLAY_TEST_REPO_ROOT/scripts/bootstrap-agent.sh" \
      "$VERLAY_TEST_REPO_ROOT/scripts/install-verlay-linux.sh" \
      "$VERLAY_TEST_REPO_ROOT/scripts/validate-agent-skill.sh" \
      "$destination/scripts/"
    install -m 0644 \
      "$VERLAY_TEST_REPO_ROOT/skills/verlay/SKILL.md" \
      "$destination/skills/verlay/SKILL.md"
    install -m 0644 \
      "$VERLAY_TEST_REPO_ROOT/skills/verlay/references/command-reference.md" \
      "$VERLAY_TEST_REPO_ROOT/skills/verlay/references/security.md" \
      "$destination/skills/verlay/references/"
    install -m 0644 \
      "$VERLAY_TEST_REPO_ROOT/packaging/linux/verlayd.service" \
      "$destination/packaging/linux/verlayd.service"
    exit 0
    ;;
  curl)
    output=""
    url=""
    while (($#)); do
      case "$1" in
        --output)
          output="$2"
          shift
          ;;
        http://*|https://*)
          url="$1"
          ;;
      esac
      shift
    done
    [[ -n "$output" && -n "$url" ]]
    asset="${url##*/}"
    if [[ "$asset" == "verlay-checksums.txt" ]]; then
      digest="$(sha256sum "$VERLAY_TEST_RELEASE_BINARY" | awk '{print $1}')"
      printf '%s  %s\n' "$digest" "$VERLAY_TEST_RELEASE_ASSET" >"$output"
    else
      [[ "$asset" == "$VERLAY_TEST_RELEASE_ASSET" ]]
      install -m 0600 "$VERLAY_TEST_RELEASE_BINARY" "$output"
    fi
    exit 0
    ;;
esac

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d /tmp/verlay-bootstrap-test.XXXXXX)"
fake_bin="$test_root/fake-bin"
release_binary="$test_root/release-binary"
trap 'rm -rf -- "$test_root"' EXIT

mkdir -p "$fake_bin" "$test_root/home" "$test_root/config" \
  "$test_root/data" "$test_root/run" "$test_root/install"
ln -s "$repo_root/scripts/test-bootstrap-agent.sh" "$fake_bin/git"
ln -s "$repo_root/scripts/test-bootstrap-agent.sh" "$fake_bin/curl"
(
  cd "$repo_root"
  go build -o "$release_binary" ./cmd/verlay
)

case "$(uname -m)" in
  x86_64|amd64) release_asset="verlay-linux-amd64" ;;
  aarch64|arm64) release_asset="verlay-linux-arm64" ;;
  *)
    printf 'Unsupported test architecture: %s\n' "$(uname -m)" >&2
    exit 1
    ;;
esac

printf 'piped bootstrap installs a non-executable release download\n'
env \
  HOME="$test_root/home" \
  XDG_CONFIG_HOME="$test_root/config" \
  XDG_DATA_HOME="$test_root/data" \
  XDG_RUNTIME_DIR="$test_root/run" \
  VERLAY_INSTALL_ROOT="$test_root/install" \
  VERLAY_SKILL_HOME="$test_root/home/.agents/skills" \
  VERLAY_SKILL_METHOD=copy \
  VERLAY_INSTALL_START_SERVICE=0 \
  VERLAY_TEST_REPO_ROOT="$repo_root" \
  VERLAY_TEST_RELEASE_BINARY="$release_binary" \
  VERLAY_TEST_RELEASE_ASSET="$release_asset" \
  PATH="$fake_bin:/usr/local/bin:/usr/bin:/bin" \
  bash -s -- --version verlay-v0.1.0-beta.1 \
  <"$repo_root/scripts/bootstrap-agent.sh"

"$test_root/install/bin/verlay" --json version >/dev/null
"$test_root/install/bin/verlay" --json schema >/dev/null
[[ -x "$test_root/install/bin/verlay" ]]
[[ "$(readlink "$test_root/install/bin/verlayd")" == "verlay" ]]
[[ -f "$test_root/home/.agents/skills/verlay/SKILL.md" ]]

(
  cd /tmp
  PATH="$test_root/install/bin:/usr/local/bin:/usr/bin:/bin"
  command -v verlay >/dev/null
  verlay --json version >/dev/null
)

printf 'PASS: piped release bootstrap works from any directory\n'
