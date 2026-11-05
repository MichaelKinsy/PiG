#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Resolve the selected Go toolchain before changing HOME so a tool-manager shim cannot select or install a different compiler inside the temporary home.
go_root="${GOROOT:-$(go env GOROOT)}"
case "$OSTYPE" in
  msys*|cygwin*) go_root=$(cygpath -u "$go_root") ;;
esac
export PATH="$go_root/bin:$PATH"
# Keep compiler caches separate from the validated program's runtime state.
export GOCACHE="${GOCACHE:-$(go env GOCACHE)}"
export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
# rustup and cargo locate the installed toolchain, its default and the registry cache through these two directories, which default to the caller's home. Pin them
# to the caller's directories before HOME moves, so a validated program that builds Rust finds the toolchain the caller installed; an explicit value is kept.
rust_base="$HOME"
case "$OSTYPE" in
  msys*|cygwin*) rust_base=$(cygpath -m "${USERPROFILE:-$HOME}") ;;
esac
export RUSTUP_HOME="${RUSTUP_HOME:-$rust_base/.rustup}"
export CARGO_HOME="${CARGO_HOME:-$rust_base/.cargo}"

directory=$(mktemp -d "${TMPDIR:-${TMP:-/tmp}}/pig-validation.XXXXXXXX")
# The pinned Pi build the program installs under this home is made read-only on purpose (automation/pin/oracle.py protect, so an npm ci through a link cannot empty it), and rm cannot remove an entry of a directory without its write bit, as with Go's module cache. oracle.py thaw is the inverse; this is the same step for the whole home.
trap 'chmod -R u+w -- "$directory" 2>/dev/null || true; rm -rf -- "$directory"' EXIT
home=$directory
# Native Windows programs need drive-qualified paths, not MSYS mount paths.
case "$OSTYPE" in
  msys*|cygwin*) home=$(cygpath -m "$home") ;;
esac

export HOME="$home"
export USERPROFILE="$home"
export APPDATA="$home/.config"
export LOCALAPPDATA="$home/.local/share"
export XDG_CONFIG_HOME="$home/.config"
export PIG_HOME="$home/.pig"
# Both overrides take precedence over HOME; preserve shared-directory mode without inheriting either store.
export PIG_CODING_AGENT_DIR="$home/.pig/agent"
export PI_CODING_AGENT_DIR="$home/.pi/agent"
# A telemetry sidecar can outlive the compiler and recreate a removed home. Disable it only in this private configuration, never in an inherited telemetry directory.
unset TEST_TELEMETRY_DIR
go telemetry off

"$@"
