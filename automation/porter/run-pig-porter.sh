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

"$PIG_BIN" piglet validate "$PROFILE"
exec "$PIG_BIN" --piglet "$PROFILE" "$@"
