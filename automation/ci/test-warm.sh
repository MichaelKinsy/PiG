#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# make test-warm: build every fixture cache once so later test runs are warm.
#   - the Go build cache for the flag sets test children use (go test, pig build, packed cells, the Porter extension)
#   - the persistent Cargo targets of the packages that build Rust SDK cells, and the pinned Pi's env daemon
# Node oracle dependencies are handled by make parity-deps, which test-prereqs already runs; there are no Python venvs
# under go test (make test-sdk-py owns its own). Safe to rerun: a warm run only revalidates.
set -euo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

echo "[test-warm] Go build cache"
go list -export -f '' ./... >/dev/null
go list -trimpath -export -f '' ./... >/dev/null
CGO_ENABLED=0 go list -trimpath -export -f '' ./... >/dev/null
(cd piglets/porter/extensions/pig-porter && GOWORK=off CGO_ENABLED=0 go list -trimpath -export -f '' ./... >/dev/null)

if ! command -v cargo >/dev/null 2>&1; then
  echo "[test-warm] cargo not found; skipping Rust caches" >&2
  exit 0
fi
sdk="$ROOT/extensions/sdk-rs"
# A Rust cell depends on pig-sdk by absolute path, and Cargo keys a path dependency's unit on that path: the SDK built as the
# root package of its own manifest is a different unit, so every cell would compile the SDK again (measured: cell tests took
# 92-98 s against 103-118 s cold with that build, and 10-16 s with the cell's own unit). The dependencies are therefore built
# through a cell crate rendered by runtimecell, the code that renders a real cell's manifest and lock copy.
cell=$(mktemp -d)
trap 'rm -rf "$cell"' EXIT
go run ./automation/ci/cargocell "$sdk" "$cell"
# One target per package that builds Rust cells; the names match the SharedCargoTargetDir calls in each package's TestMain.
for pkg in subprocess runtimecell cli conformance; do
  target=$(go run ./automation/ci/cargotarget "$pkg" "$sdk")
  echo "[test-warm] Rust SDK cell dependencies -> $target"
  (cd "$cell" && CARGO_TARGET_DIR="$target" cargo build --release --quiet)
done

daemon="$ROOT/.upstream/current/packages/env/daemon"
if [[ -f "$daemon/Cargo.toml" ]]; then
  target=$(go run ./automation/ci/cargotarget env-oracle "$daemon")
  echo "[test-warm] Pi env daemon -> $target"
  (cd "$daemon" && CARGO_TARGET_DIR="$target" cargo build --release --locked --quiet)
fi
echo "[test-warm] done"
