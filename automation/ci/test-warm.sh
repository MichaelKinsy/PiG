#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# make test-warm: build every fixture cache once so later test runs are warm.
#   - the Go build cache for the flag sets test children use (go test, pig build, packed cells, the Porter extension)
#   - the persistent Cargo targets of the packages that build Rust SDK cells, and the pinned Pi's env daemon
# Pass --rust-only [pkg ...] to warm only the named Rust targets (or all of them when none are named), skipping the Go
# build cache walks: macOS and Windows jobs run that before their tests so no test compiles the Rust SDK cold inside its
# 120 s wait budget.
# Node oracle dependencies are handled by make parity-deps, which test-prereqs already runs; there are no Python venvs
# under go test (make test-sdk-py owns its own). Safe to rerun: a warm run only revalidates.
set -euo pipefail
ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

rust_only=0
rust_pkgs=()
if [[ "${1:-}" == "--rust-only" ]]; then
  rust_only=1
  shift
  rust_pkgs=("$@")
fi

if [[ "$rust_only" -eq 0 ]]; then
  echo "[test-warm] Go build cache"
  go list -export -f '' ./... >/dev/null
  go list -trimpath -export -f '' ./... >/dev/null
  CGO_ENABLED=0 go list -trimpath -export -f '' ./... >/dev/null
  (cd piglets/porter/extensions/pig-porter && GOWORK=off CGO_ENABLED=0 go list -trimpath -export -f '' ./... >/dev/null)
fi
if ! command -v cargo >/dev/null 2>&1; then
  echo "[test-warm] cargo not found; skipping Rust caches" >&2
  exit 0
fi
sdk="$ROOT/extensions/sdk-rs"
daemon="$ROOT/.upstream/current/packages/env/daemon"
# Under Git Bash on Windows, $ROOT and mktemp -d are POSIX paths (/d/..., /tmp/...) that Go and Cargo cannot open directly; convert to Windows native paths (matching Go's filepath.Abs inside testenv.SharedCargoTargetDir) when cygpath is present.
native_path() {
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$1"
  else
    printf '%s\n' "$1"
  fi
}
sdk_native=$(native_path "$sdk")
# A Rust cell depends on pig-sdk by absolute path, and Cargo keys a path dependency's unit on that path: the SDK built as the
# root package of its own manifest is a different unit, so every cell would compile the SDK again (measured: cell tests took
# 92-98 s against 103-118 s cold with that build, and 10-16 s with the cell's own unit). The dependencies are therefore built
# through a cell crate rendered by runtimecell, the code that renders a real cell's manifest and lock copy.
cell=$(mktemp -d)
trap 'rm -rf "$cell"' EXIT
cell_native=$(native_path "$cell")
go run ./automation/ci/cargocell "$sdk_native" "$cell_native"
# One target per package that builds Rust cells; the names match the SharedCargoTargetDir calls in each package's TestMain.
want_pkg() {
  local p="$1" selected
  if [[ ${#rust_pkgs[@]} -eq 0 ]]; then
    return 0
  fi
  for selected in "${rust_pkgs[@]}"; do
    [[ "$selected" == "$p" ]] && return 0
  done
  return 1
}
for pkg in subprocess runtimecell cli conformance; do
  want_pkg "$pkg" || continue
  target=$(go run ./automation/ci/cargotarget "$pkg" "$sdk_native")
  echo "[test-warm] Rust SDK cell dependencies -> $target"
  (cd "$cell" && CARGO_TARGET_DIR="$target" cargo build --release --quiet)
done

if [[ -f "$daemon/Cargo.toml" ]] && want_pkg env-oracle; then
  daemon_native=$(native_path "$daemon")
  target=$(go run ./automation/ci/cargotarget env-oracle "$daemon_native")
  echo "[test-warm] Pi env daemon -> $target"
  (cd "$daemon" && CARGO_TARGET_DIR="$target" cargo build --release --locked --quiet)
fi
echo "[test-warm] done"
