#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

usage() {
  echo "usage: automation/ci/build-ci-image.sh <go|parity>" >&2
  echo "  RESOLVE_APK_LOCK=1   write the package index's current versions of the locked packages to the image's packages.lock, build nothing" >&2
  echo "  VERIFY_IMAGE_REF=<ref>  skip the build and run the tool-version checks against this existing image (a published image by digest)" >&2
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
verify_ref="${VERIFY_IMAGE_REF:-}"
resolve_lock="${RESOLVE_APK_LOCK:-}"
[ -z "$verify_ref" ] || [ -z "$resolve_lock" ] || { echo "VERIFY_IMAGE_REF and RESOLVE_APK_LOCK are exclusive" >&2; exit 2; }

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

resolved_dir=
cleanup() {
  [ -z "${builder:-}" ] || docker buildx rm "$builder" >/dev/null 2>&1 || true
  [ -z "$resolved_dir" ] || rm -rf "$resolved_dir"
}
trap cleanup EXIT

# build_image builds the Dockerfile for the image: by default into the local daemon as $ref, and with RESOLVE_APK_LOCK into
# $resolved_dir, which then holds only the resolved packages.lock (the Dockerfile's apk-resolved stage).
build_image() {
  local -a result_args load_args=()
  if [ -n "$resolve_lock" ]; then
    result_args=(--target apk-resolved --output "type=local,dest=$resolved_dir")
  else
    result_args=(--label "org.opencontainers.image.revision=$revision" --label "org.opencontainers.image.source=https://github.com/MichaelKinsy/PiG" -t "$ref")
    load_args=(--load)
  fi
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
      "${result_args[@]}" \
      -f "$root/automation/images/$image_dir/Dockerfile" \
      "${load_args[@]}" \
      "$root"
  else
    docker build --platform "$platform" \
      "${build_args[@]}" \
      "${result_args[@]}" \
      -f "$root/automation/images/$image_dir/Dockerfile" \
      "$root"
  fi
}

if [ -n "$verify_ref" ]; then
  ref="$verify_ref"
  echo "Checking $ref for $platform without building it"
elif [ -n "$resolve_lock" ]; then
  resolved_dir="$(mktemp -d)"
  echo "Resolving the apk lock of $image for $platform from the current package index"
  build_image
  test -s "$resolved_dir/packages.lock"
  cp "$resolved_dir/packages.lock" "$root/automation/images/$image_dir/packages.lock"
  echo "Wrote automation/images/$image_dir/packages.lock"
  exit 0
else
  echo "Building $ref for $platform"
  build_image
fi

go_version="go$(tr -d '\r\n' < "$root/.go-version")"
[ "$go_version" != go ] || { echo ".go-version has no Go pin" >&2; exit 2; }

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
    pi_version="$(python3 "$root/automation/pin/pin.py" get package_version)"
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
