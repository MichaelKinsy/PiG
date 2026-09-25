#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
OUT_DIR=${PIG_TEST_FIXTURE_DIR:-"$ROOT/tmp/test-fixtures"}
FIXTURE_EXT_BIN="$OUT_DIR/fixture-ext"
SDK_FIXTURE_BIN="$OUT_DIR/sdk-fixture"
CONFORMANCE_SDK_FIXTURE_BIN="$OUT_DIR/conformance-sdk-fixture"
RUST_TARGET=${CARGO_TARGET_DIR:-"$OUT_DIR/rust-target"}
if [[ "$RUST_TARGET" != /* ]]; then
  RUST_TARGET="$ROOT/$RUST_TARGET"
fi
RUST_SDK_FIXTURE_BIN="$OUT_DIR/rust-sdk-fixture"

mkdir -p "$OUT_DIR"

# build_go_fixture <output-binary> <source-dir> [extra-env ...]
# Always relink through the Go build cache. A timestamp check over only the
# fixture directory misses SDK and host dependencies, leaving tests pointed at
# a stale executable after the contract under test changes.
build_go_fixture() {
  local out="$1" src="$2"
  shift 2
  echo "[test-fixtures] building $(basename "$out") → $out" >&2
  (cd "$src" && env "$@" go build -o "$out" .)
}

# Fixtures inside the root module build through the repository's go.work so
# they always compile against the local extensions/sdk. The root go.mod
# requires the SDK at its release version with no replace (go install needs
# that), so building them with GOWORK=off would download the published SDK,
# or fail before the release tag exists. Standalone fixture modules
# (sdk-fixture) pin the local SDK with their own replace and build with
# GOWORK=off.
# Workspace mode accepts only -mod=readonly or -mod=vendor.
root_goflags=()
for flag in ${GOFLAGS:-}; do
  [[ $flag == -mod=mod ]] || root_goflags+=("$flag")
done
ROOT_GOFLAGS="GOFLAGS=${root_goflags[*]}"
ROOT_GOWORK="GOWORK=$ROOT/go.work"

build_go_fixture "$FIXTURE_EXT_BIN" \
  ./coding/extension/host/subprocess/testdata/fixture-ext/ \
  "$ROOT_GOWORK" "$ROOT_GOFLAGS"

build_go_fixture "$SDK_FIXTURE_BIN" \
  ./coding/extension/host/subprocess/testdata/sdk-fixture/ \
  CGO_ENABLED=0 \
  GOWORK=off

build_go_fixture "$CONFORMANCE_SDK_FIXTURE_BIN" \
  ./tests/extension-conformance/testfixture/cmd/ \
  CGO_ENABLED=0 \
  "$ROOT_GOWORK" "$ROOT_GOFLAGS"

# Rust fixture: cargo handles its own incremental cache via target-dir.
RUST_EXPORT=""
RUST_TARGET_EXPORT=""
RUST_REPORT=""
if command -v cargo >/dev/null 2>&1; then
  RUST_SRC="$ROOT/tests/extension-conformance/testdata/rust-sdk-fixture"
  RUST_RELEASE="$RUST_TARGET/release/rust-sdk-fixture"
  echo "[test-fixtures] building rust-sdk-fixture → $RUST_SDK_FIXTURE_BIN" >&2
  (cd "$RUST_SRC" && CARGO_TARGET_DIR="$RUST_TARGET" cargo build --release --quiet)
  cp "$RUST_RELEASE" "$RUST_SDK_FIXTURE_BIN"
  RUST_EXPORT="export PIG_TEST_RUST_SDK_FIXTURE_BIN=\"$RUST_SDK_FIXTURE_BIN\""
  RUST_TARGET_EXPORT="export CARGO_TARGET_DIR=\"$RUST_TARGET\""
  RUST_REPORT="rust-sdk-fixture=$RUST_SDK_FIXTURE_BIN"
else
  echo "[test-fixtures] skipped: rust-sdk-fixture (cargo not found)" >&2
  RUST_REPORT="rust-sdk-fixture=(skipped: cargo not found)"
fi

if [[ "${1:-}" == "--exports" ]]; then
  cat <<EOF
export PIG_TEST_FIXTURE_EXT_BIN="$FIXTURE_EXT_BIN"
export PIG_TEST_SDK_FIXTURE_BIN="$SDK_FIXTURE_BIN"
export PIG_TEST_CONFORMANCE_SDK_FIXTURE_BIN="$CONFORMANCE_SDK_FIXTURE_BIN"
$RUST_EXPORT
$RUST_TARGET_EXPORT
EOF
else
  cat <<EOF
fixture-ext=$FIXTURE_EXT_BIN
sdk-fixture=$SDK_FIXTURE_BIN
conformance-sdk-fixture=$CONFORMANCE_SDK_FIXTURE_BIN
$RUST_REPORT
EOF
fi
