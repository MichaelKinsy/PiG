#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
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

directory=$(mktemp -d "${TMPDIR:-${TMP:-/tmp}}/pig-validation.XXXXXXXX")
trap 'rm -rf -- "$directory"' EXIT
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
