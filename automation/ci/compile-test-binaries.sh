#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Usage: compile-test-binaries.sh <output-dir> [package-pattern...]
#
# Compile every package's test binary with the default flags, running none, so the Go build
# cache holds what the test jobs build. Each binary gets a name derived from its import path,
# because go test -c -o <dir>/ ./... refuses when two packages share a basename. Any compile
# error fails the script. PIG_TEST_COMPILE_JOBS bounds concurrency (default 4).
set -euo pipefail
if [[ $# -lt 1 ]]; then
  echo "usage: $0 <output-dir> [package-pattern...]" >&2
  exit 2
fi
out=$1
shift
patterns=("$@")
[[ ${#patterns[@]} -gt 0 ]] || patterns=(./...)
jobs=${PIG_TEST_COMPILE_JOBS:-4}
mkdir -p "$out"

listing=$(go list -f '{{if or .TestGoFiles .XTestGoFiles}}{{.ImportPath}}{{end}}' "${patterns[@]}")
mapfile -t packages < <(printf '%s\n' "$listing" | grep . || true)
if [[ ${#packages[@]} -eq 0 ]]; then
  echo "compile-test-binaries: no packages with tests match ${patterns[*]}" >&2
  exit 1
fi

compile() {
  local pkg=$1 out=$2
  go test -c -o "$out/${pkg//\//_}.test" "$pkg"
}
export -f compile

# shellcheck disable=SC2016 # the child bash expands $1 and $2: the package and output dir xargs passes after _.
printf '%s\n' "${packages[@]}" | xargs -P "$jobs" -I{} bash -c 'compile "$1" "$2"' _ {} "$out"
echo "compile-test-binaries: compiled ${#packages[@]} test binaries into $out"
