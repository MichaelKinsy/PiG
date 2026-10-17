#!/bin/sh
# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT
#
# Install the TinyGo release the Durable core builds with (the tinygo row of automation/dev/toolchains.lock) and print the path
# of its binary. The archive's SHA-256 must equal the lock's digest. An installed release is reused.
#   TINYGO=$(sh durable/core/budget/tinygo-install.sh) sh durable/core/budget/build-wasm.sh tinygo ...
# Environment: DCORE_TINYGO_CACHE (default ${XDG_CACHE_HOME:-$HOME/.cache}/pig-dev/tinygo).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
lock=$here/../../../automation/dev/toolchains.lock
cache=${DCORE_TINYGO_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/pig-dev/tinygo}
die() { echo "tinygo-install.sh: $*" >&2; exit 1; }
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in x86_64 | amd64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch=$(uname -m) ;; esac
row=$(awk -F'\t' -v o="$os" -v a="$arch" '!/^#/ && $1 == "tinygo" && $3 == o && $4 == a { print; exit }' "$lock")
[ -n "$row" ] || die "no tinygo row for $os/$arch in automation/dev/toolchains.lock"
version=$(printf '%s\n' "$row" | cut -f2)
url=$(printf '%s\n' "$row" | cut -f6)
want=$(printf '%s\n' "$row" | cut -f7)

bin=$cache/$version/tinygo/bin/tinygo
if [ ! -x "$bin" ]; then
	command -v curl >/dev/null 2>&1 || die "curl is needed to download $url"
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/tinygo.XXXXXXXX")
	trap 'rm -rf "$tmp"' EXIT
	echo "tinygo-install.sh: downloading $url" >&2
	curl -fsSL -m 900 -o "$tmp/tinygo.tar.gz" "$url" || die "download of $url failed"
	[ "$(sha256 "$tmp/tinygo.tar.gz")" = "$want" ] || die "checksum mismatch for $url"
	mkdir "$tmp/x" && tar -xzf "$tmp/tinygo.tar.gz" -C "$tmp/x"
	mkdir -p "$cache" && rm -rf "${cache:?}/$version" && mv "$tmp/x" "$cache/$version"
fi
printf '%s\n' "$bin"
