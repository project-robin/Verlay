#!/usr/bin/env bash
set -euo pipefail

repository="${VERLAY_REPOSITORY:-project-robin/Verlay}"
version="${VERLAY_VERSION:-latest}"
install_runtime=1
install_skill=1
start_service="${MESH_INSTALL_START_SERVICE:-1}"
skill_method="${VERLAY_SKILL_METHOD:-auto}"
install_source="${VERLAY_INSTALL_SOURCE:-auto}"
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

if [[ -n "$script_dir" &&
      -f "$script_dir/../go.mod" &&
      -f "$script_dir/../skills/verlay/SKILL.md" ]]; then
  source_root="$(cd "$script_dir/.." && pwd)"
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

"$source_root/scripts/validate-agent-skill.sh"

if [[ "$install_runtime" == "1" ]]; then
  printf 'Installing the Verlay runtime...\n'
    VERLAY_REPOSITORY="$repository" \
    VERLAY_VERSION="$version" \
    VERLAY_INSTALL_SOURCE="$install_source" \
    VERLAY_INSTALL_START_SERVICE="$start_service" \
    "$source_root/scripts/install-verlay-linux.sh"
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
  elif ((${#agent_hosts[@]})); then
    printf 'Agent host flags require npx; the standard skill was installed at %s.\n' \
      "$canonical_skill" >&2
  fi
fi

verlay_binary="${VERLAY_INSTALL_ROOT:-${MESH_INSTALL_ROOT:-$HOME/.local}}/bin/verlay"
if [[ "$install_runtime" == "1" ]]; then
  "$verlay_binary" --json version
  "$verlay_binary" --json schema >/dev/null
  if [[ "$start_service" == "1" ]]; then
    "$verlay_binary" --json status
    "$verlay_binary" --json doctor
  fi
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
