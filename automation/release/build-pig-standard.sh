#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# build-pig-standard.sh SOURCE_ROOT OUT_DIR WORK_DIR
#
# Builds the PiG Standard Piglet Binary for the host target with the build every
# Piglet Binary uses:
#
#   pig piglet build piglets/standard/pig-standard.yaml --format binary --builder native
#
# VERSION (PiG's version, no v prefix), GOOS and GOARCH come from the
# environment, as in release-candidate.yml. The native builder compiles only the
# host target (`pig piglet build` refuses another one), so the target must be
# the host: release-candidate.yml runs this on a native runner per target.
#
# SOURCE_ROOT is a clean Git checkout of the release commit: the builder records
# the commit it builds from and refuses a tree that is not a Git checkout. The
# `pig` that drives the build is compiled from SOURCE_ROOT into WORK_DIR and is
# not shipped. The Binary is compiled from the same SOURCE_ROOT
# (PIG_SOURCE_ROOT), with CGO disabled and -trimpath like the pig archive's
# binary. Its Piglet release version is the manifest's release.version, which
# must equal VERSION. OUT_DIR and WORK_DIR must lie outside SOURCE_ROOT, which
# the builder hashes.
#
# Writes OUT_DIR/pig-standard[.exe] and OUT_DIR/pig-standard.sha256 (a
# sha256sum line for that file). Prints "binary=<path>" for $GITHUB_OUTPUT.
set -euo pipefail

source_root=${1:?usage: build-pig-standard.sh SOURCE_ROOT OUT_DIR WORK_DIR}
out=${2:?usage: build-pig-standard.sh SOURCE_ROOT OUT_DIR WORK_DIR}
work=${3:?usage: build-pig-standard.sh SOURCE_ROOT OUT_DIR WORK_DIR}
: "${VERSION:?}" "${GOOS:?}" "${GOARCH:?}"

exe=
if [ "$GOOS" = windows ]; then exe=.exe; fi
manifest=piglets/standard/pig-standard.yaml

host=$(go env GOHOSTOS)/$(go env GOHOSTARCH)
if [ "$host" != "$GOOS/$GOARCH" ]; then
  echo "build-pig-standard: the native Piglet builder builds only its host target $host, not $GOOS/$GOARCH; run on a $GOOS/$GOARCH runner" >&2
  exit 1
fi
if ! grep -qx "  version: $VERSION" "$source_root/$manifest"; then
  echo "build-pig-standard: $manifest does not declare release version $VERSION (make set-version updates it)" >&2
  exit 1
fi
if ! status=$(git -C "$source_root" status --porcelain) || [ -n "$status" ]; then
  echo "build-pig-standard: $source_root is not a clean Git checkout; the Piglet build records the commit it builds from" >&2
  exit 1
fi

rm -rf "${work:?}" "$out"
mkdir -p "$work/builder" "$work/home" "$out"
work=$(cd "$work" && pwd)
out=$(cd "$out" && pwd)
source_root=$(cd "$source_root" && pwd)

export CGO_ENABLED=0 GOTOOLCHAIN=local
(
  cd "$source_root"
  go build -trimpath -buildvcs=false -o "$work/builder/pig$exe" ./cmd/pig
  PIG_HOME="$work/home" PIG_SOURCE_ROOT="$source_root" "$work/builder/pig$exe" piglet build "$manifest" \
    --format binary --builder native --targets "$GOOS/$GOARCH" \
    --out "$out/pig-standard$exe" --no-input
)
(cd "$out" && sha256sum "pig-standard$exe" > pig-standard.sha256)
printf 'binary=%s\n' "$out/pig-standard$exe"
