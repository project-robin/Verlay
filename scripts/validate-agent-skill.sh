#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
skill_dir="${1:-$repo_root/skills/verlay}"
skill_file="$skill_dir/SKILL.md"

fail() {
  printf 'Agent Skill validation failed: %s\n' "$*" >&2
  exit 1
}

[[ -f "$skill_file" ]] || fail "$skill_file does not exist"

name="$(awk '
  NR == 1 && $0 == "---" { frontmatter = 1; next }
  frontmatter && $0 == "---" { exit }
  frontmatter && /^name:[[:space:]]*/ {
    sub(/^name:[[:space:]]*/, "")
    print
    exit
  }
' "$skill_file")"

description="$(awk '
  NR == 1 && $0 == "---" { frontmatter = 1; next }
  frontmatter && $0 == "---" { exit }
  frontmatter && /^description:[[:space:]]*/ {
    sub(/^description:[[:space:]]*/, "")
    print
    exit
  }
' "$skill_file")"

[[ -n "$name" ]] || fail "frontmatter is missing name"
[[ -n "$description" ]] || fail "frontmatter is missing description"
[[ "$name" == "$(basename "$skill_dir")" ]] ||
  fail "name '$name' must match parent directory '$(basename "$skill_dir")'"
[[ "$name" =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]] ||
  fail "name must contain lowercase letters, digits, and single hyphens only"
(( ${#name} <= 64 )) || fail "name exceeds 64 characters"
(( ${#description} <= 1024 )) || fail "description exceeds 1024 characters"

while IFS= read -r reference; do
  [[ -e "$skill_dir/$reference" ]] ||
    fail "referenced file '$reference' does not exist"
done < <(
  sed -n 's/.*](\\([^)]*\\)).*/\\1/p' "$skill_file" |
    awk '!/^(https?:|#)/ && !seen[$0]++'
)

printf 'Valid Agent Skill: %s\n' "$skill_file"
