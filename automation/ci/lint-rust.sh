#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Lint every Rust crate the repository tracks: `cargo fmt --check` and `cargo clippy --all-targets -- -D warnings`.
# The SDK, the example extension, the conformance and parity fixtures and the benchmark extension are all Rust crates, so one rule covers them.
# Needs the rustfmt and clippy components of the pinned toolchain (CI: `rustup toolchain install "$RUST_VERSION" --profile minimal --component rustfmt --component clippy`).
#
# Some crates reach the SDK in a form that does not resolve where they sit in the tree: the parity fixtures name the published `pig-sdk` version
# (the harness points it at the checkout when it builds them) and the benchmark extension names a path that is right only where the harness
# stages it. The first are linted with the checkout's SDK patched in; the second are linted from a copy staged at that path.
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

for component in fmt clippy; do
  if ! cargo "$component" --version >/dev/null 2>&1; then
    echo "lint-rust: cargo $component is missing; run: rustup component add rustfmt clippy" >&2
    exit 1
  fi
done

export CARGO_TARGET_DIR=${CARGO_TARGET_DIR:-"$ROOT/build/lint-rust-target"}
case "$CARGO_TARGET_DIR" in
  /* | [A-Za-z]:[\\/]*) ;;
  *) CARGO_TARGET_DIR="$ROOT/$CARGO_TARGET_DIR" ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

status=0
crates=0
while IFS= read -r manifest; do
  dir=$(dirname "$manifest")
  crates=$((crates + 1))
  echo "lint-rust: $dir"
  if ! (cd "$dir" && cargo fmt --check); then
    echo "lint-rust: $dir is not rustfmt-clean (run cargo fmt in it)" >&2
    status=1
  fi
  lint_dir="$dir"
  patch=()
  sdk_line=$(grep -E '^pig-sdk[[:space:]]*=' "$manifest" || true)
  case "$sdk_line" in
    *'path = "'*)
      sdk_path=${sdk_line#*path = \"}
      sdk_path=${sdk_path%%\"*}
      if [ ! -d "$dir/$sdk_path" ]; then
        # The harness stages the crate at examples/extensions/<name> of a tree whose extensions/ is the checkout's.
        mkdir -p "$stage/examples/extensions"
        ln -sfn "$ROOT/extensions" "$stage/extensions"
        name=$(basename "$(dirname "$dir")")-$(basename "$dir")
        rm -rf "$stage/examples/extensions/$name"
        cp -R "$dir" "$stage/examples/extensions/$name"
        rm -rf "$stage/examples/extensions/$name/target"
        lint_dir="$stage/examples/extensions/$name"
      fi
      ;;
    *'= "'*) patch=(--config "patch.crates-io.pig-sdk.path=\"$ROOT/extensions/sdk-rs\"") ;;
  esac
  # A crate that tracks no Cargo.lock must not leave one behind.
  lock=
  if [ "$lint_dir" = "$dir" ] && [ ! -e "$dir/Cargo.lock" ]; then lock="$dir/Cargo.lock"; fi
  if ! (cd "$lint_dir" && cargo clippy ${patch[@]+"${patch[@]}"} --all-targets --quiet -- -D warnings); then
    echo "lint-rust: clippy rejected $dir" >&2
    status=1
  fi
  if [ -n "$lock" ]; then rm -f "$lock"; fi
done < <(git ls-files '*Cargo.toml' | sort)

if [ "$crates" -eq 0 ]; then
  echo "lint-rust: no tracked Cargo.toml found" >&2
  exit 1
fi
exit "$status"
