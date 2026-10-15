#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PROFILE="$ROOT/piglets/porter/pig-porter.yaml"
PIG_BIN="${PIG_BIN:-$ROOT/bin/pig-parity}"

[ -x "$PIG_BIN" ] || {
  echo "pig-porter: Pig binary is not executable: $PIG_BIN" >&2
  echo "build it with: cd $ROOT && make parity-bin" >&2
  exit 1
}

# shellcheck source=automation/porter/hermetic-agent.sh
. "$ROOT/automation/porter/hermetic-agent.sh"
home="$(mktemp -d "${TMPDIR:-/tmp}/pig-porter-home.XXXXXXXX")"
trap 'chmod -R u+rwX "$home" 2>/dev/null || true; rm -rf "$home"' EXIT
porter_export_hermetic_agent "$home/agent"

"$PIG_BIN" piglet validate "$PROFILE"
"$PIG_BIN" --piglet "$PROFILE" "$@"
