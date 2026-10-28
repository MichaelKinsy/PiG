#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Installs the pinned SBCL into PREFIX (default $HOME/.cache/pig-sbcl/<pin>) from automation/dev/toolchains.lock. Where SBCL publishes a binary
# of the pinned version for this OS and architecture (Linux amd64) the binary is installed; where it does not (arm64 macOS) the checksummed
# release source is built, bootstrapped by the older official binary the lock names. Every download is checked against its lock digest before
# it is used; nothing is piped to a shell. A prefix that already holds this pin is reused. Prints the prefix on the last line.
# Building from source needs make and a C compiler.
#   install-sbcl.sh [PREFIX]
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock="$root/automation/dev/toolchains.lock"

row() { awk -F'\t' -v t="$1" -v o="$2" -v a="$3" '!/^#/ && $1==t && $3==o && $4==a {print; exit}' "$lock"; }
case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "install-sbcl.sh: unsupported OS $(uname -s)" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "install-sbcl.sh: unsupported architecture $(uname -m)" >&2; exit 1 ;; esac

sha256_stdin() { if command -v sha256sum >/dev/null 2>&1; then sha256sum; else shasum -a 256; fi; }
src_row=$(row sbcl-source any any); boot_row=$(row sbcl "$os" "$arch")
[ -n "$src_row" ] && [ -n "$boot_row" ] || { echo "install-sbcl.sh: toolchains.lock has no sbcl row for $os/$arch" >&2; exit 1; }
version=$(cut -f2 <<<"$src_row")
pin=$(printf '%s\n%s\n' "$src_row" "$boot_row" | sha256_stdin | cut -c1-16)
prefix=${1:-${PIG_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}}/pig-sbcl/$version-$pin}

if [ -x "$prefix/bin/sbcl" ] && [ -f "$prefix/.pin" ] && [ "$(cat "$prefix/.pin")" = "$pin" ]; then
  echo "$prefix"; exit 0
fi

sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
fetch() { # url sha256 dest
  curl -fsSL --retry 3 -o "$3" "$1"
  [ "$(sha256 "$3")" = "$2" ] || { echo "install-sbcl.sh: $1 does not match its pinned sha256 $2" >&2; exit 1; }
}

work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
fetch "$(cut -f6 <<<"$boot_row")" "$(cut -f7 <<<"$boot_row")" "$work/boot.tar.bz2"
mkdir "$work/boot"
tar -xjf "$work/boot.tar.bz2" -C "$work/boot" --strip-components=1

if [ "$(cut -f2 <<<"$boot_row")" = "$version" ]; then
  INSTALL_ROOT="$prefix" sh -c 'cd "$1" && sh install.sh' _ "$work/boot" >/dev/null
else
  fetch "$(cut -f6 <<<"$src_row")" "$(cut -f7 <<<"$src_row")" "$work/source.tar.bz2"
  mkdir "$work/source"
  tar -xjf "$work/source.tar.bz2" -C "$work/source" --strip-components=1
  (cd "$work/boot" && INSTALL_ROOT="$work/boot-install" sh install.sh >/dev/null)
  (
    cd "$work/source"
    export SBCL_HOME="$work/boot-install/lib/sbcl"
    sh make.sh --prefix="$prefix" --fancy \
      --xc-host="$work/boot-install/bin/sbcl --no-userinit --no-sysinit" >"$work/build.log" 2>&1 \
      || { tail -60 "$work/build.log" >&2; exit 1; }
    unset SBCL_HOME
    INSTALL_ROOT="$prefix" sh install.sh >>"$work/build.log" 2>&1
  )
fi
SBCL_HOME="$prefix/lib/sbcl" "$prefix/bin/sbcl" --noinform --non-interactive --no-sysinit --no-userinit \
  --eval '(unless (and (find :sb-thread *features*) (string= (lisp-implementation-version) "'"$version"'")) (sb-ext:exit :code 1))' \
  || { echo "install-sbcl.sh: the install is not SBCL $version with threads" >&2; exit 1; }
printf '%s' "$pin" >"$prefix/.pin"
echo "$prefix"
