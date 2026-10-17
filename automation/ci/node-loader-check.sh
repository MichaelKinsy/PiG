#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# The Node loader check CI runs under each Node version: TypeScript package loading and the runtime preflight, in a throwaway HOME.
# Keep Go's module and build caches outside the throwaway HOME: the module cache is read-only by design, and reusing it avoids a second download.
GOMODCACHE="$(go env GOMODCACHE)"
GOCACHE="$(go env GOCACHE)"
export GOMODCACHE GOCACHE
home=$(mktemp -d)
trap 'rm -rf "$home"' EXIT
export HOME="$home" PIG_HOME="$home/.pig"
export PIG_CODING_AGENT_DIR="$home/pig-agent" PI_CODING_AGENT_DIR="$home/pi-agent"
node --version
go test ./coding/extension/host/subprocess -count=1 -run '^(TestNodeLoaderUsesSynchronousHooksWhenAvailable|TestNodeIssuePackageForms|TestNodeRuntimePreflight.*)$'
