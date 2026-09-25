#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# End-to-end local test of the npm distribution, with no registry access:
#   1. cross-compile the six release targets (CGO_ENABLED=0, as release-candidate.yml does)
#      and lay them out as release archives plus SHA256SUMS;
#   2. generate and `npm pack` the seven packages with pack_npm.py;
#   3. install the launcher and the host platform package into a temp prefix and
#      run `pig --version` through the npm-installed launcher;
#   4. install the launcher alone and check its missing-platform-package error.
#
# Usage: automation/release/npm/e2e-local.sh [WORKDIR]
set -euo pipefail

repo=$(cd "$(dirname "$0")/../../.." && pwd)
work=${1:-$(mktemp -d "${TMPDIR:-/tmp}/pig-npm-e2e.XXXXXX")}
version=$(sed -nE 's/^const PigVersion = "([^"]+)"$/\1/p' "$repo/coding/pigversion/pigversion.go")
release="$work/release"
stage="$work/stage"
npm_out="$work/npm"
mkdir -p "$release" "$stage" "$npm_out"
echo "e2e: version $version, workdir $work"

for target in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64; do
  goos=${target%-*}
  goarch=${target#*-}
  name="pig-${version}-${target}"
  binary=pig
  if [ "$goos" = windows ]; then binary=pig.exe; fi
  mkdir -p "$stage/$name"
  (cd "$repo" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
    -ldflags "-s -w -X main.Build=npm-e2e" -o "$stage/$name/$binary" ./cmd/pig) &
done
wait
for target in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64; do
  name="pig-${version}-${target}"
  cp "$repo/LICENSE" "$repo/NOTICE" "$repo/THIRD_PARTY_NOTICES.md" "$stage/$name/"
  if [[ "$target" = windows-* ]]; then
    (cd "$stage" && python3 -m zipfile -c "$release/$name.zip" "$name")
  else
    tar -C "$stage" -czf "$release/$name.tar.gz" "$name"
  fi
done
(cd "$release" && python3 "$repo/automation/release/combine-checksums.py" . >/dev/null)

python3 "$repo/automation/release/npm/pack_npm.py" --archives "$release" --version "$version" --out "$npm_out"

case "$(uname -s)" in
  Darwin) host_os=darwin ;;
  Linux) host_os=linux ;;
  *) host_os=win32 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) host_cpu=x64 ;;
  arm64 | aarch64) host_cpu=arm64 ;;
  *) echo "e2e: unsupported host cpu $(uname -m)" >&2; exit 1 ;;
esac
launcher_tgz=$(awk '$1 == "@pi-in-go/pig" {print $2}' "$npm_out/publish-order.txt")
host_tgz=$(awk -v n="@pi-in-go/pig-${host_os}-${host_cpu}" '$1 == n {print $2}' "$npm_out/publish-order.txt")
test "$(wc -l <"$npm_out/publish-order.txt" | tr -d ' ')" = 7
test "$(tail -n 1 "$npm_out/publish-order.txt" | cut -d' ' -f1)" = "@pi-in-go/pig"

# --omit=optional keeps npm from asking the registry for the five other
# (unpublished) platform packages; the host package is installed explicitly.
prefix="$work/prefix"
export npm_config_cache="$work/npm-cache" npm_config_audit=false npm_config_fund=false npm_config_update_notifier=false
npm install -g --prefix "$prefix" --omit=optional "$npm_out/$launcher_tgz" "$npm_out/$host_tgz" >/dev/null
out=$("$prefix/bin/pig" --version)
echo "e2e: npm-installed pig --version -> $out"
case "$out" in *"$version"*) ;; *) echo "e2e: FAIL version mismatch" >&2; exit 1 ;; esac
"$prefix/bin/pig" --help >/dev/null
set +e
"$prefix/bin/pig" --definitely-not-a-flag >/dev/null 2>&1
status=$?
set -e
test "$status" -ne 0 || { echo "e2e: FAIL non-zero exit not forwarded" >&2; exit 1; }

bare="$work/prefix-bare"
npm install -g --prefix "$bare" --omit=optional "$npm_out/$launcher_tgz" >/dev/null
set +e
err=$("$bare/bin/pig" --version 2>&1)
status=$?
set -e
test "$status" -eq 1
case "$err" in *"pig-${host_os}-${host_cpu} is not installed"*) ;; *) echo "e2e: FAIL unexpected error: $err" >&2; exit 1 ;; esac
echo "e2e: missing platform package error -> ok"
echo "e2e: PASS"
