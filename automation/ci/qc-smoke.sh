#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

PIG_BIN=${PIG_BIN:-$ROOT/bin/pig-parity}
if [[ ! -x "$PIG_BIN" ]]; then
  echo "ERROR: PIG_BIN is not executable: $PIG_BIN" >&2
  echo "Run: make parity-bin" >&2
  exit 2
fi

echo "qc-smoke: pig=$PIG_BIN"
out=$(mktemp -d "${TMPDIR:-/tmp}/pig-qc.XXXXXXXX")
trap 'rm -rf "$out"' EXIT
"$PIG_BIN" --version >"$out"/version.txt
PIG_HOME=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX") "$PIG_BIN" docs sync >"$out"/docs-sync.txt
PIG_HOME=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX") "$PIG_BIN" docs list >"$out"/docs-list.txt
grep -q 'providers.md' "$out"/docs-list.txt
"$PIG_BIN" install examples/extensions/go-factory --validate-only --json >"$out"/go-factory.json
grep -q '"valid": true' "$out"/go-factory.json
grep -q '"hello"' "$out"/go-factory.json

PIG_PARITY_PIG_BIN="$PIG_BIN" go test -tags=parity ./parity/runner \
  -run 'TestParity/(01-version-flag|10-login-subscription-providers|01-register-handshake)$' \
  -count=1 -v -timeout 5m

echo "qc-smoke: ok"
