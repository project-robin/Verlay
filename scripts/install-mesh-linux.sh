#!/usr/bin/env bash
set -euo pipefail

printf 'install-mesh-linux.sh is deprecated; using install-verlay-linux.sh.\n' >&2
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install-verlay-linux.sh" "$@"
