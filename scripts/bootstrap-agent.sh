#!/usr/bin/env bash
set -euo pipefail

repository="${VERLAY_REPOSITORY:-project-robin/Verlay}"
version="${VERLAY_VERSION:-latest}"
install_runtime=1
install_skill=1
start_service="${VERLAY_INSTALL_START_SERVICE:-1}"
skill_method="${VERLAY_SKILL_METHOD:-auto}"
install_source="${VERLAY_INSTALL_SOURCE:-auto}"
reset_legacy=0
declare -a agent_hosts=()

usage() {
  cat <<'EOF'
Install the Verlay runtime and register its Agent Skill.

Usage: bootstrap-agent.sh [options]

Options:
  --runtime-only          Install verlay/verlayd without registering the Agent Skill
  --skill-only            Register the Agent Skill without installing Verlay
  --agent HOST            Register for one agent host (repeatable; '*' means all)
  --skill-method METHOD   auto, copy, or skills-cli (default: auto)
  --source SOURCE         auto, checkout, or release (default: auto)
  --version VERSION       Release tag or branch to install (default: latest)
  --repository OWNER/REPO Source repository (default: project-robin/Verlay)
  --no-start              Install verlay/verlayd without starting verlayd
  --reset-legacy          Delete state from a detected legacy mesh installation
  -h, --help              Show this help

The bootstrap never pairs devices, approves verification codes, authorizes automatic
receiving, changes relays, or grants filesystem access.
EOF
}

fail() {
  printf 'Verlay bootstrap failed: %s\n' "$*" >&2
  exit 1
}

while (($#)); do
  case "$1" in
    --runtime-only)
      install_skill=0
      ;;
    --skill-only)
      install_runtime=0
      ;;
    --agent)
      (($# >= 2)) || fail "--agent requires a host name"
      agent_hosts+=("$2")
      shift
      ;;
    --skill-method)
      (($# >= 2)) || fail "--skill-method requires auto, copy, or skills-cli"
      skill_method="$2"
      shift
      ;;
    --source)
      (($# >= 2)) || fail "--source requires auto, checkout, or release"
      install_source="$2"
      shift
      ;;
    --version)
      (($# >= 2)) || fail "--version requires a tag or branch"
      version="$2"
      shift
      ;;
    --repository)
      (($# >= 2)) || fail "--repository requires OWNER/REPO"
      repository="$2"
      shift
      ;;
    --no-start)
      start_service=0
      ;;
    --reset-legacy)
      reset_legacy=1
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option '$1'"
      ;;
  esac
  shift
done

case "$skill_method" in
  auto|copy|skills-cli) ;;
  *) fail "--skill-method must be auto, copy, or skills-cli" ;;
esac

case "$install_source" in
  auto|checkout|release) ;;
  *) fail "--source must be auto, checkout, or release" ;;
esac
if [[ "$reset_legacy" == "1" && "$install_runtime" != "1" ]]; then
  fail "--reset-legacy requires a runtime installation"
fi
if [[ "$reset_legacy" == "1" && "$start_service" != "1" ]]; then
  fail "--reset-legacy cannot be combined with --no-start"
fi

host_os="$(uname -s)"
if [[ "$install_runtime" == "1" && "$host_os" != "Linux" ]]; then
  case "$host_os" in
    MINGW*|MSYS*|CYGWIN*)
      fail 'Windows detected. Open PowerShell and run .\scripts\bootstrap-agent.ps1'
      ;;
    *)
      fail "unsupported operating system '$host_os'; do not improvise an installation"
      ;;
  esac
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || true)"
source_root=""
temporary_checkout=""
local_checkout=0

if [[ -n "$script_dir" &&
      -f "$script_dir/../go.mod" &&
      -f "$script_dir/../skills/verlay/SKILL.md" ]]; then
  source_root="$(cd "$script_dir/.." && pwd)"
  local_checkout=1
else
  command -v git >/dev/null ||
    fail "git is required when bootstrap-agent.sh is run outside a checkout"
  temporary_checkout="$(mktemp -d "${TMPDIR:-/tmp}/verlay-bootstrap.XXXXXX")"
  trap 'rm -rf -- "$temporary_checkout"' EXIT
  if [[ "$version" == "latest" ]]; then
    git clone --quiet --depth 1 "https://github.com/$repository.git" "$temporary_checkout/repo"
  else
    git clone --quiet --depth 1 --branch "$version" \
      "https://github.com/$repository.git" "$temporary_checkout/repo"
  fi
  source_root="$temporary_checkout/repo"
fi
if [[ "$install_source" == "auto" && "$local_checkout" != "1" ]]; then
  install_source=release
fi

"$source_root/scripts/validate-agent-skill.sh"

if [[ "$install_runtime" == "1" ]]; then
  printf 'Installing the Verlay runtime...\n'
  installer_args=()
  if [[ "$reset_legacy" == "1" ]]; then
    installer_args+=(--reset-legacy)
  fi
    VERLAY_REPOSITORY="$repository" \
    VERLAY_VERSION="$version" \
    VERLAY_INSTALL_SOURCE="$install_source" \
    VERLAY_INSTALL_START_SERVICE="$start_service" \
    "$source_root/scripts/install-verlay-linux.sh" "${installer_args[@]}"
fi

skill_home="${VERLAY_SKILL_HOME:-$HOME/.agents/skills}"
canonical_skill="$skill_home/verlay"

if [[ "$install_skill" == "1" ]]; then
  printf 'Installing the standard Verlay Agent Skill...\n'
  mkdir -p "$canonical_skill/references"
  install -m 0644 "$source_root/skills/verlay/SKILL.md" "$canonical_skill/SKILL.md"
  install -m 0644 "$source_root/skills/verlay/references/command-reference.md" \
    "$canonical_skill/references/command-reference.md"
  install -m 0644 "$source_root/skills/verlay/references/security.md" \
    "$canonical_skill/references/security.md"

  if ((${#agent_hosts[@]} == 0)); then
    command -v codex >/dev/null 2>&1 && agent_hosts+=(codex)
    command -v pi >/dev/null 2>&1 && agent_hosts+=(pi)
    command -v claude >/dev/null 2>&1 && agent_hosts+=(claude-code)
    command -v cursor >/dev/null 2>&1 && agent_hosts+=(cursor)
    command -v opencode >/dev/null 2>&1 && agent_hosts+=(opencode)
    command -v copilot >/dev/null 2>&1 && agent_hosts+=(github-copilot)
    command -v gemini >/dev/null 2>&1 && agent_hosts+=(gemini-cli)
  fi

  use_skills_cli=0
  if [[ "$skill_method" == "skills-cli" ]]; then
    command -v npx >/dev/null ||
      fail "npx is required for --skill-method skills-cli"
    use_skills_cli=1
  elif [[ "$skill_method" == "auto" ]] && command -v npx >/dev/null; then
    use_skills_cli=1
  fi

  if [[ "$use_skills_cli" == "1" ]]; then
    skills_args=(
      --yes skills add "$source_root"
      --skill verlay
      --global
      --copy
      --yes
    )
    for host in "${agent_hosts[@]}"; do
      skills_args+=(--agent "$host")
    done
    if ! DISABLE_TELEMETRY=1 npx "${skills_args[@]}"; then
      if [[ "$skill_method" == "skills-cli" ]]; then
        fail "the Skills CLI could not register Verlay"
      fi
      printf 'Skills CLI registration failed; the standard skill remains at %s.\n' \
        "$canonical_skill" >&2
    fi
  elif [[ "$skill_method" != "copy" ]] && ((${#agent_hosts[@]})); then
    printf 'Skills CLI unavailable; known detected hosts will receive direct copies from %s.\n' \
      "$canonical_skill" >&2
  fi

  for host in "${agent_hosts[@]}"; do
    case "$host" in
      codex) host_skill="$HOME/.codex/skills/verlay" ;;
      pi) host_skill="$HOME/.pi/agent/skills/verlay" ;;
      claude-code) host_skill="$HOME/.claude/skills/verlay" ;;
      cursor) host_skill="$HOME/.cursor/skills/verlay" ;;
      opencode) host_skill="${XDG_CONFIG_HOME:-$HOME/.config}/opencode/skills/verlay" ;;
      github-copilot) host_skill="$HOME/.copilot/skills/verlay" ;;
      gemini-cli) host_skill="$HOME/.gemini/skills/verlay" ;;
      *) continue ;;
    esac
    mkdir -p "$host_skill/references"
    install -m 0644 "$source_root/skills/verlay/SKILL.md" "$host_skill/SKILL.md"
    install -m 0644 "$source_root/skills/verlay/references/command-reference.md" \
      "$host_skill/references/command-reference.md"
    install -m 0644 "$source_root/skills/verlay/references/security.md" \
      "$host_skill/references/security.md"
  done
fi

verlay_binary="${VERLAY_INSTALL_ROOT:-$HOME/.local}/bin/verlay"
if [[ "$install_runtime" == "1" ]]; then
  binary_dir="$(dirname "$verlay_binary")"
  case ":$PATH:" in
    *":$binary_dir:"*) ;;
    *)
      shell_name="$(basename "${SHELL:-sh}")"
      case "$shell_name" in
        fish)
          profile="$HOME/.config/fish/conf.d/verlay.fish"
          profile_line="fish_add_path --path \"$binary_dir\""
          ;;
        zsh)
          profile="$HOME/.zprofile"
          profile_line="export PATH=\"$binary_dir:\$PATH\""
          ;;
        *)
          profile="$HOME/.profile"
          profile_line="export PATH=\"$binary_dir:\$PATH\""
          ;;
      esac
      mkdir -p "$(dirname "$profile")"
      if [[ ! -f "$profile" ]] || ! grep -Fq '# Verlay CLI' "$profile"; then
        {
          printf '\n# Verlay CLI\n'
          printf '%s\n' "$profile_line"
        } >>"$profile"
      fi
      export PATH="$binary_dir:$PATH"
      printf 'Added %s to the user PATH in %s.\n' "$binary_dir" "$profile"
      ;;
  esac
  "$verlay_binary" --json version
  "$verlay_binary" --json schema >/dev/null
  if [[ "$start_service" == "1" ]]; then
    "$verlay_binary" --json status
    "$verlay_binary" --json doctor
  fi
  (
    cd "${TMPDIR:-/tmp}"
    command -v verlay >/dev/null
    verlay --json version >/dev/null
  )
fi

printf '\nVerlay bootstrap complete.\n'
if [[ "$install_runtime" == "1" ]]; then
  printf 'CLI: %s\n' "$verlay_binary"
  printf 'Daemon: %s\n' "$(dirname "$verlay_binary")/verlayd"
fi
if [[ "$install_skill" == "1" ]]; then
  printf 'Agent Skill: %s\n' "$canonical_skill"
  printf 'Start a new agent session if the current host does not reload skills automatically.\n'
fi
printf 'Security: no device was paired or authorized. A human must approve the first pairing.\n'
