#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
set -euo pipefail

# Installs the pinned SBCL into PREFIX (default $HOME/.cache/pig-sbcl/<pin>): the release source of automation/dev/toolchains.lock built
# with :sb-core-compression and zstd linked statically, bootstrapped by the official binary the lock names for this OS and architecture.
# Every download is checked against its lock digest before it is used; nothing is piped to a shell. A prefix that already holds this pin is
# reused. Prints the prefix on the last line. Needs make, a C compiler, zstd headers and libzstd.a (apt: libzstd-dev; brew: zstd).
#   install-sbcl.sh [PREFIX]
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock="$root/automation/dev/toolchains.lock"

row() { awk -F'\t' -v t="$1" -v o="$2" -v a="$3" '!/^#/ && $1==t && $3==o && $4==a {print; exit}' "$lock"; }
case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) echo "install-sbcl.sh: unsupported OS $(uname -s)" >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) echo "install-sbcl.sh: unsupported architecture $(uname -m)" >&2; exit 1 ;; esac

src_row=$(row sbcl-source any any); boot_row=$(row sbcl "$os" "$arch")
[ -n "$src_row" ] && [ -n "$boot_row" ] || { echo "install-sbcl.sh: toolchains.lock has no sbcl row for $os/$arch" >&2; exit 1; }
version=$(cut -f2 <<<"$src_row")
pin=$(printf '%s\n%s\n' "$src_row" "$boot_row" | sha256sum 2>/dev/null | cut -c1-16 || printf '%s\n%s\n' "$src_row" "$boot_row" | shasum -a 256 | cut -c1-16)
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
fetch "$(cut -f6 <<<"$src_row")" "$(cut -f7 <<<"$src_row")" "$work/source.tar.bz2"
fetch "$(cut -f6 <<<"$boot_row")" "$(cut -f7 <<<"$boot_row")" "$work/boot.tar.bz2"
mkdir "$work/source" "$work/boot"
tar -xjf "$work/source.tar.bz2" -C "$work/source" --strip-components=1
tar -xjf "$work/boot.tar.bz2" -C "$work/boot" --strip-components=1
(cd "$work/boot" && INSTALL_ROOT="$work/boot-install" sh install.sh >/dev/null)

# zstd is linked statically: a directory holding only libzstd.a comes first on the link path, so -lzstd resolves to it.
zdir="$work/zstd-static"; mkdir "$zdir"
for candidate in /usr/lib/x86_64-linux-gnu /usr/lib/aarch64-linux-gnu /usr/lib64 /usr/lib /opt/homebrew/lib /usr/local/lib; do
  if [ -f "$candidate/libzstd.a" ]; then cp "$candidate/libzstd.a" "$zdir/"; break; fi
done
brew_prefix=$(command -v brew >/dev/null 2>&1 && brew --prefix zstd 2>/dev/null || true)
[ -f "$zdir/libzstd.a" ] || { [ -n "$brew_prefix" ] && [ -f "$brew_prefix/lib/libzstd.a" ] && cp "$brew_prefix/lib/libzstd.a" "$zdir/"; } || true
[ -f "$zdir/libzstd.a" ] || { echo "install-sbcl.sh: libzstd.a not found (apt install libzstd-dev, or brew install zstd)" >&2; exit 1; }
extra_cflags=""; [ -n "$brew_prefix" ] && extra_cflags="-I$brew_prefix/include"

(
  cd "$work/source"
  export SBCL_HOME="$work/boot-install/lib/sbcl"
  # LIBRARY_PATH puts the directory first for the compiler driver's -lzstd, whatever the runtime's link line does with LDFLAGS.
  LIBRARY_PATH="$zdir${LIBRARY_PATH:+:$LIBRARY_PATH}" CFLAGS="${CFLAGS:-} $extra_cflags" LDFLAGS="-L$zdir ${LDFLAGS:-}" \
    sh make.sh --prefix="$prefix" --fancy --with-sb-core-compression \
      --xc-host="$work/boot-install/bin/sbcl --no-userinit --no-sysinit" >"$work/build.log" 2>&1 \
    || { tail -60 "$work/build.log" >&2; exit 1; }
  unset SBCL_HOME
  INSTALL_ROOT="$prefix" sh install.sh >>"$work/build.log" 2>&1
)
SBCL_HOME="$prefix/lib/sbcl" "$prefix/bin/sbcl" --noinform --non-interactive --no-sysinit --no-userinit \
  --eval '(unless (and (find :sb-core-compression *features*) (find :sb-thread *features*) (string= (lisp-implementation-version) "'"$version"'")) (sb-ext:exit :code 1))' \
  || { echo "install-sbcl.sh: the build is not SBCL $version with threads and core compression" >&2; exit 1; }
# the runtime must not depend on a shared libzstd, so a compressed image starts on a machine without one
if command -v ldd >/dev/null 2>&1 && ldd "$prefix/bin/sbcl" | grep -q libzstd; then
  echo "install-sbcl.sh: $prefix/bin/sbcl links libzstd dynamically; the static archive was not used" >&2; exit 1
fi
if command -v otool >/dev/null 2>&1 && otool -L "$prefix/bin/sbcl" | grep -q libzstd; then
  echo "install-sbcl.sh: $prefix/bin/sbcl links libzstd dynamically; the static archive was not used" >&2; exit 1
fi
printf '%s' "$pin" >"$prefix/.pin"
echo "$prefix"
