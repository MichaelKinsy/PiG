#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail

usage() {
  echo "usage: automation/ci/build-ci-image.sh <go|parity>" >&2
  exit 2
}

image="${1:-}"
shift || true
[ "$#" -eq 0 ] || usage

case "$image" in
  go) image_dir=ci-go ;;
  parity) image_dir=ci-parity ;;
  *) usage ;;
esac

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
. "$root/automation/images/$image_dir/metadata.env"

registry="${REGISTRY:-ghcr.io/michaelkinsy}"
platform="${PLATFORM:-linux/amd64}"
revision="${SOURCE_REVISION:-$(git -C "$root" rev-parse HEAD)}"
case "$revision" in
  *[!0-9a-fA-F]*|'') echo "SOURCE_REVISION must be a Git object ID" >&2; exit 2 ;;
esac
ref="$registry/$IMAGE_NAME:$IMAGE_TAG-${revision:0:12}"

declare -a build_args=()
for variable in GO_IMAGE GO_BUILDER WOLFI_BASE HTTP_PROXY HTTPS_PROXY NO_PROXY http_proxy https_proxy no_proxy; do
  value="${!variable:-}"
  [ -n "$value" ] && build_args+=(--build-arg "$variable=$value")
done
for variable in http_proxy https_proxy no_proxy; do
  upper="${variable^^}"
  if [ -z "${!variable:-}" ] && [ -n "${!upper:-}" ]; then
    build_args+=(--build-arg "$variable=${!upper}")
  fi
done

echo "Building $ref for $platform"
if [ -n "${CI_BASE_DIR:-}" ]; then
  declare -a build_hosts=()
  declare -A mapped_hosts=()
  for variable in HTTP_PROXY HTTPS_PROXY; do
    proxy="${!variable:-}"
    [ -n "$proxy" ] || continue
    host="$(python3 -c 'import sys; from urllib.parse import urlparse; print(urlparse(sys.argv[1]).hostname or "")' "$proxy")"
    [ -n "$host" ] || { echo "$variable has no hostname" >&2; exit 2; }
    [ -z "${mapped_hosts[$host]:-}" ] || continue
    address="$(getent ahostsv4 "$host" | awk 'NR == 1 { print $1 }')"
    [ -n "$address" ] || { echo "cannot resolve proxy host: $host" >&2; exit 1; }
    build_hosts+=(--add-host "$host:$address")
    mapped_hosts[$host]=1
  done

  builder="pig-ci-${GITHUB_RUN_ID:-local}-${GITHUB_RUN_ATTEMPT:-0}-${image}-$$"
  cleanup_builder() {
    docker buildx rm "$builder" >/dev/null 2>&1 || true
  }
  trap cleanup_builder EXIT

  case "$image" in
    go)
      build_args+=(
        --build-arg GO_IMAGE=pig-ci-go-base
        --build-context "pig-ci-go-base=oci-layout://$CI_BASE_DIR/go"
      )
      ;;
    parity)
      build_args+=(
        --build-arg GO_BUILDER=pig-ci-go-builder
        --build-arg WOLFI_BASE=pig-ci-wolfi-base
        --build-context "pig-ci-go-builder=oci-layout://$CI_BASE_DIR/go-builder"
        --build-context "pig-ci-wolfi-base=oci-layout://$CI_BASE_DIR/wolfi"
      )
      ;;
  esac

  docker buildx create \
    --name "$builder" \
    --driver docker-container \
    --driver-opt network=host \
    --driver-opt image=pig-ci-base-buildkit:0.32.2 >/dev/null
  docker buildx inspect --bootstrap "$builder" >/dev/null
  docker buildx build --builder "$builder" --platform "$platform" \
    "${build_args[@]}" \
    "${build_hosts[@]}" \
    --label "org.opencontainers.image.revision=$revision" \
    --label "org.opencontainers.image.source=https://github.com/MichaelKinsy/PiG" \
    -f "$root/automation/images/$image_dir/Dockerfile" \
    -t "$ref" \
    --load \
    "$root"
else
  docker build --platform "$platform" \
    "${build_args[@]}" \
    --label "org.opencontainers.image.revision=$revision" \
    --label "org.opencontainers.image.source=https://github.com/MichaelKinsy/PiG" \
    -f "$root/automation/images/$image_dir/Dockerfile" \
    -t "$ref" \
    "$root"
fi

go_version="$(awk '$1 == "toolchain" { print $2; exit }' "$root/go.mod")"
[ -n "$go_version" ] || { echo "go.mod has no toolchain pin" >&2; exit 2; }

case "$image" in
  go)
    docker run --rm --platform "$platform" "$ref" bash -euo pipefail -c '
      test "$(go env GOVERSION)" = "$1"
      git --version
    ' -- "$go_version"
    ;;
  parity)
    node_version="$(tr -d '\r\n' < "$root/.node-version")"
    npm_version="$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1]))["dependencies"]["npm"])' "$root/automation/images/npm-runtime/package.json")"
    rust_version="$(awk '$1 == "RUST_VERSION:" { print $2; exit }' "$root/.github/workflows/ci.yml")"
    pi_version="$(awk -F'"' '/^const UpstreamVersion = "/ { print $2; exit }' "$root/coding/pigversion/pigversion.go")"
    python_version="$(awk -F= '/^python-[0-9]+\.[0-9]+=/ { sub(/^python-/, "", $1); print $1; exit }' "$root/automation/images/ci-parity/packages.lock")"
    for pin in "$node_version" "$npm_version" "$rust_version" "$pi_version" "$python_version"; do
      [ -n "$pin" ] || { echo "missing parity toolchain pin" >&2; exit 2; }
    done
    docker run --rm --platform "$platform" "$ref" bash -euo pipefail -c '
      test "$(go env GOVERSION)" = "$1"
      test "$(node --version)" = "v$2"
      test "$(npm --version)" = "$3"
      test "$(rustc --version | cut -d" " -f2)" = "$4"
      test "$(pi --version)" = "$5"
      python3 -c '"'"'import sys; assert ".".join(map(str, sys.version_info[:2])) == sys.argv[1]'"'"' "$6"
      tmux -V
    ' -- "$go_version" "$node_version" "$npm_version" "$rust_version" "$pi_version" "$python_version"
    ;;
esac

printf '%s\n' "$ref"
