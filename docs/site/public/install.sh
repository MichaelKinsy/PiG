#!/bin/sh
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# PiG installer for macOS and Linux.
#
#   curl -fsSL https://pi-in-go.dev/install.sh | sh
#
# Downloads the PiG release archive for this machine from GitHub Releases,
# verifies it against the release's SHA256SUMS, and installs the pig binary.
# It fails closed: no published release, a missing checksum, or a checksum
# mismatch stops the install before anything is written to the install
# directory.
#
# Environment:
#   PIG_VERSION        install this version (for example 0.2.0) instead of the latest
#   PIG_INSTALL_DIR    install directory (default: $HOME/.local/bin)
#   PIG_API_BASE       API that names the latest release (default: https://pi-in-go.dev/api)
#   PIG_DOWNLOAD_BASE  release download root (default: https://github.com/MichaelKinsy/PiG/releases/download)
#
# The whole script is one function called on its last line, so a truncated
# download runs nothing.

set -eu

main() {
  api_base=${PIG_API_BASE:-https://pi-in-go.dev/api}
  download_base=${PIG_DOWNLOAD_BASE:-https://github.com/MichaelKinsy/PiG/releases/download}
  install_dir=${PIG_INSTALL_DIR:-${HOME:?HOME is not set}/.local/bin}

  need uname
  need tar
  need mktemp
  downloader=$(pick_downloader)
  platform=$(detect_platform)

  version=${PIG_VERSION:-}
  if [ -z "$version" ]; then
    version=$(latest_version "$downloader" "$api_base")
  fi
  version=${version#v}
  valid_version "$version" || fail "not a release version: $version"

  name="pig-${version}-${platform}"
  archive="${name}.tar.gz"
  release_url="${download_base%/}/v${version}"

  work=$(mktemp -d 2>/dev/null || mktemp -d -t pig-install)
  trap 'rm -rf "$work"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  say "Downloading PiG ${version} for ${platform}"
  fetch "$downloader" "${release_url}/SHA256SUMS" "$work/SHA256SUMS" ||
    fail "PiG ${version} has no SHA256SUMS at ${release_url}; refusing to install an unverified archive"
  fetch "$downloader" "${release_url}/${archive}" "$work/$archive" ||
    fail "could not download ${release_url}/${archive}"

  expected=$(expected_sha256 "$work/SHA256SUMS" "$archive")
  [ -n "$expected" ] || fail "SHA256SUMS has no single valid entry for ${archive}"
  actual=$(sha256_of "$work/$archive")
  [ "$actual" = "$expected" ] || fail "checksum mismatch for ${archive}: expected ${expected}, got ${actual}"
  say "Verified SHA-256 ${actual}"

  mkdir "$work/extract"
  tar -xzf "$work/$archive" -C "$work/extract"
  binary="$work/extract/${name}/pig"
  if [ ! -f "$binary" ] || [ -L "$binary" ]; then
    fail "${archive} does not contain ${name}/pig"
  fi

  # Stage beside the destination (a noexec /tmp cannot run the smoke test),
  # then replace any existing pig in one rename.
  mkdir -p "$install_dir"
  staged="${install_dir}/.pig.install.$$"
  trap 'rm -rf "$work" "$staged"' EXIT
  cp "$binary" "$staged"
  chmod 0755 "$staged"
  "$staged" --version >/dev/null 2>&1 || fail "the downloaded pig binary does not run on this machine"
  mv -f "$staged" "${install_dir}/pig"

  say "Installed $("${install_dir}/pig" --version 2>/dev/null || printf 'pig %s' "$version") to ${install_dir}/pig"
  case ":${PATH:-}:" in
    *":${install_dir}:"*) ;;
    *) say "Add ${install_dir} to PATH to run pig, for example: export PATH=\"${install_dir}:\$PATH\"" ;;
  esac
}

say() {
  printf 'pig-install: %s\n' "$*"
}

fail() {
  printf 'pig-install: error: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "this installer needs '$1'"
}

pick_downloader() {
  if command -v curl >/dev/null 2>&1; then
    echo curl
  elif command -v wget >/dev/null 2>&1; then
    echo wget
  else
    fail "this installer needs curl or wget"
  fi
}

# fetch DOWNLOADER URL FILE: HTTPS only, failing on any HTTP error.
fetch() {
  case "$1" in
    curl) curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "$3" "$2" ;;
    wget) wget --https-only -q -O "$3" "$2" ;;
  esac
}

detect_platform() {
  case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "unsupported operating system $(uname -s); download a release archive from GitHub instead" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "unsupported CPU architecture $(uname -m)" ;;
  esac
  # A Rosetta 2 shell on Apple silicon reports x86_64; install the native binary.
  if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
    [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
    arch=arm64
  fi
  echo "${os}-${arch}"
}

latest_version() {
  response=$(fetch_stdout "$1" "${2%/}/latest-version") ||
    fail "no PiG release is published yet (${2%/}/latest-version did not answer); build from source or set PIG_VERSION"
  latest=$(printf '%s' "$response" | tr -d '\n\r' | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
  [ -n "$latest" ] || fail "could not read the latest PiG version from ${2%/}/latest-version"
  echo "$latest"
}

fetch_stdout() {
  case "$1" in
    curl) curl --proto '=https' --tlsv1.2 -fsSL --retry 3 "$2" ;;
    wget) wget --https-only -q -O - "$2" ;;
  esac
}

# valid_version VERSION: SemVer core with optional pre-release and build parts.
valid_version() {
  printf '%s\n' "$1" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
}

# expected_sha256 SUMS NAME prints the digest of NAME (listed as NAME or ./NAME)
# only when exactly one well-formed line names it.
expected_sha256() {
  awk -v name="$2" '
    ($2 == name || $2 == "./" name || $2 == "*" name || $2 == "*./" name) && NF == 2 {
      count++
      digest = tolower($1)
    }
    END {
      if (count == 1 && digest ~ /^[0-9a-f]+$/ && length(digest) == 64) print digest
    }
  ' "$1"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print tolower($1)}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print tolower($1)}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 -r "$1" | awk '{print tolower($1)}'
  else
    fail "this installer needs sha256sum, shasum, or openssl to verify the download"
  fi
}

main "$@"
