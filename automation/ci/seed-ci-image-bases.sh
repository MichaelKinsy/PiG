#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
set -euo pipefail

case "${1:-}" in
  go) bases=(go buildkit syft grype) ;;
  parity) bases=(go-builder wolfi buildkit syft grype) ;;
  *) echo "usage: automation/ci/seed-ci-image-bases.sh <go|parity>" >&2; exit 2 ;;
esac

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
dockerfile_base() {
  local file="$1" argument="$2" value
  value="$(awk -v key="$argument" 'index($0, "ARG " key "=") == 1 { print substr($0, length(key) + 6); exit }' "$root/automation/images/$file/Dockerfile")"
  if [[ ! "$value" =~ @sha256:[0-9a-f]{64}$ ]]; then
    echo "missing digest-pinned $argument in $file/Dockerfile" >&2
    return 1
  fi
  printf '%s\n' "$value"
}

platform="${PLATFORM:-linux/amd64}"
if [ "$platform" != "linux/amd64" ]; then
  echo "unsupported seed platform: $platform" >&2
  exit 2
fi

base_dir="${CI_BASE_DIR:-}"
if [ -z "$base_dir" ]; then
  echo "CI_BASE_DIR is required" >&2
  exit 2
fi
case "$base_dir" in
  /|.) echo "unsafe CI_BASE_DIR: $base_dir" >&2; exit 2 ;;
esac
if [ -e "$base_dir" ]; then
  if [ ! -f "$base_dir/.pig-ci-base-dir" ]; then
    echo "refusing to replace unowned CI_BASE_DIR: $base_dir" >&2
    exit 2
  fi
  rm -rf "$base_dir"
fi
mkdir -p "$base_dir"
touch "$base_dir/.pig-ci-base-dir"

temporary="$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")"
trap 'rm -rf "$temporary"' EXIT
crane_archive="$temporary/crane.tar.gz"
crane="$temporary/crane"
curl -fsSL \
  https://github.com/google/go-containerregistry/releases/download/v0.20.6/go-containerregistry_Linux_x86_64.tar.gz \
  -o "$crane_archive"
echo 'c1d593d01551f2c9a3df5ca0a0be4385a839bd9b86d4a76e18d7b17d16559127  '"$crane_archive" | sha256sum -c - >/dev/null
tar -xzf "$crane_archive" -C "$temporary" crane

pull_context() {
  local source="$1" name="$2"
  "$crane" pull --platform "$platform" --format oci --annotate-ref "$source" "$base_dir/$name"
}

load_image() {
  local source="$1" target="$2" archive="$temporary/image.tar" output loaded
  "$crane" pull --platform "$platform" "$source" "$archive"
  output="$(docker load -i "$archive")"
  loaded="${output#Loaded image: }"
  if [ "$loaded" = "$output" ]; then
    echo "cannot determine loaded image from: $output" >&2
    return 1
  fi
  docker tag "$loaded" "$target"
  if [ "$loaded" != "$target" ]; then
    docker image rm "$loaded" >/dev/null
  fi
  rm "$archive"
}

for base in "${bases[@]}"; do
  case "$base" in
    go)
      source="$(dockerfile_base ci-go GO_IMAGE)"
      pull_context "$source" go
      ;;
    go-builder)
      source="$(dockerfile_base ci-parity GO_BUILDER)"
      pull_context "$source" go-builder
      ;;
    wolfi)
      source="$(dockerfile_base ci-parity WOLFI_BASE)"
      pull_context "$source" wolfi
      ;;
    buildkit)
      load_image \
        'moby/buildkit:buildx-stable-1@sha256:28a898719c18a33f4e8000685287fa36fd0dd9560c6440227d3a732d79bb41d8' \
        'pig-ci-base-buildkit:0.32.2'
      ;;
    syft)
      load_image \
        'anchore/syft@sha256:c6d5719f48f5a5986acf2847eb1ed7c53176e712d5721fcd156184cfb262f6eb' \
        'pig-ci-base-syft:1.45.1'
      ;;
    grype)
      load_image \
        'anchore/grype@sha256:7a9fc7f89ccef78ae5a7691a115d3f0d41b1f319d589dd8cc1dcb9ab3f01dd28' \
        'pig-ci-base-grype:0.114.0'
      ;;
  esac
done
