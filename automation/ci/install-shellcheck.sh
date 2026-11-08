#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Installs the pinned ShellCheck (a release binary, sha256-verified) on a Linux runner, so security.yml and `make pre-push` analyze shell scripts with
# the same version instead of whatever apt has. `--print-version` prints the pinned version and installs nothing; pre-push-gate.sh compares its
# ShellCheck with it. SHELLCHECK_INSTALL_DIR (default /usr/local/bin) names where the binary goes; sudo is used when that directory is not writable.
# To move the pin: set version, download both tarballs from https://github.com/koalaman/shellcheck/releases/tag/v<version>, replace both digests, and run
# `make pre-push` and `make compliance`.
version=0.11.0
sha256_x86_64=8c3be12b05d5c177a04c29e3c78ce89ac86f1595681cab149b65b97c4e227198
sha256_aarch64=12b331c1d2db6b9eb13cfca64306b1b157a86eb69db83023e261eaa7e7c14588

if [ "${1:-}" = --print-version ]; then
  echo "$version"
  exit 0
fi
if [ "$#" -ne 0 ]; then
  echo "usage: automation/ci/install-shellcheck.sh [--print-version]" >&2
  exit 2
fi

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) arch=x86_64 expected=$sha256_x86_64 ;;
  Linux/aarch64 | Linux/arm64) arch=aarch64 expected=$sha256_aarch64 ;;
  *)
    echo "install-shellcheck.sh: no pinned ShellCheck $version binary for $(uname -s)/$(uname -m); install that version yourself (brew install shellcheck)" >&2
    exit 1
    ;;
esac

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
archive="shellcheck-v$version.linux.$arch.tar.xz"
# Cache, then the same tarball as a ghcr.io OCI blob (pig-ci-tools), then the upstream release asset. All three are
# held to the pinned digest by fetch-pinned.sh, which also serves a digest-keyed cache that security.yml restores.
"$(dirname "${BASH_SOURCE[0]}")/fetch-pinned.sh" "$expected" "$work/$archive" \
  "https://github.com/koalaman/shellcheck/releases/download/v$version/$archive" \
  || { echo "install-shellcheck.sh: $archive has no source with sha256 $expected (pinned)" >&2; exit 1; }
tar -xJf "$work/$archive" -C "$work" "shellcheck-v$version/shellcheck"

dir="${SHELLCHECK_INSTALL_DIR:-/usr/local/bin}"
sudo_cmd=()
if [ ! -w "$dir" ]; then sudo_cmd=(sudo); fi
"${sudo_cmd[@]}" install -m 0755 "$work/shellcheck-v$version/shellcheck" "$dir/shellcheck"

installed=$("$dir/shellcheck" --version | awk '/^version:/ {print $2}')
if [ "$installed" != "$version" ]; then
  echo "install-shellcheck.sh: installed ShellCheck reports $installed, pinned $version" >&2
  exit 1
fi
echo "ShellCheck $installed installed in $dir"
