#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Builds the history package three ways from the same source and runs its tests and build check on each:
# native Go, standard Go wasip1, and TinyGo (-target=wasip1 -scheduler=none, plus `tinygo test` natively).
# TINYGO may point at the tinygo binary (default: tinygo on PATH).
set -euo pipefail
cd "$(dirname "$0")/../../.."
TINYGO=${TINYGO:-tinygo}
out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

go vet ./durable/core/history/...
go test ./durable/core/history/...
GOOS=wasip1 GOARCH=wasm go build -o "$out/go.wasm" ./durable/core/history/internal/buildcheck
"$TINYGO" build -target=wasip1 -scheduler=none -o "$out/tinygo.wasm" ./durable/core/history/internal/buildcheck
"$TINYGO" test ./durable/core/history
cat >"$out/run.mjs" <<'JS'
import { WASI } from "node:wasi";
import { readFileSync } from "node:fs";
for (const f of process.argv.slice(2)) {
  const wasi = new WASI({ version: "preview1", args: [f], env: {}, returnOnExit: true });
  const inst = await WebAssembly.instantiate(await WebAssembly.compile(readFileSync(f)), wasi.getImportObject());
  const code = wasi.start(inst);
  if (code !== 0) { console.error(f, "exit", code); process.exit(1); }
  console.log(f.split("/").pop(), "ok");
}
JS
node --no-warnings "$out/run.mjs" "$out/go.wasm" "$out/tinygo.wasm"
ls -l "$out"/*.wasm
