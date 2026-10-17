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
# One target per package that builds Rust cells; the names match the SharedCargoTargetDir calls in each package's TestMain.
for pkg in subprocess cli conformance; do
  target=$(go run ./automation/ci/cargotarget "$pkg" "$sdk")
  echo "[test-warm] Rust SDK dependencies -> $target"
  (cd "$sdk" && CARGO_TARGET_DIR="$target" cargo build --release --locked --quiet)
done

daemon="$ROOT/.upstream/current/packages/env/daemon"
if [[ -f "$daemon/Cargo.toml" ]]; then
  target=$(go run ./automation/ci/cargotarget env-oracle "$daemon")
  echo "[test-warm] Pi env daemon -> $target"
  (cd "$daemon" && CARGO_TARGET_DIR="$target" cargo build --release --locked --quiet)
fi
echo "[test-warm] done"
