#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Prints the SHA-256 of everything a CI image is built from: its Dockerfile, package locks, metadata, and the toolchain pins the
# build and its checks read. automation/images/<image>/published.env records this value for the published image, so a pull request
# can tell whether the digest it pins was built from the inputs in its own tree, without building anything.
set -euo pipefail

usage() {
  echo "usage: automation/ci/ci-image-inputs.sh <go|parity>" >&2
  exit 2
}

[ "$#" -eq 1 ] || usage
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

case "$1" in
  go)
    files=(
      .go-version
      automation/images/ci-go/Dockerfile
      automation/images/ci-go/metadata.env
      automation/images/ci-go/packages.lock
    )
    ;;
  parity)
    files=(
      .go-version
      .node-version
      .upstream/pin
      automation/images/ci-parity/Dockerfile
      automation/images/ci-parity/metadata.env
      automation/images/ci-parity/packages.lock
      automation/images/ci-parity/package.json
      automation/images/ci-parity/package-lock.json
      automation/images/npm-runtime/package.json
      automation/images/npm-runtime/package-lock.json
    )
    ;;
  *) usage ;;
esac

digest() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi | awk '{ print $1 }'
}

{
  for file in "${files[@]}"; do
    printf '%s %s\n' "$file" "$(digest < "$root/$file")"
  done
  # The parity checks compare the image's Rust against the version the workflows pin; the pin is an input of the checks, not of the bytes.
  if [ "$1" = parity ]; then
    printf 'rust %s\n' "$(awk '$1 == "RUST_VERSION:" { print $2; exit }' "$root/.github/workflows/ci.yml")"
  fi
} | digest
