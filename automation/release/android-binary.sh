#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
#
# Build and check PiG's Android (Termux) binary.
#
#   android-binary.sh build [go build arguments...]   build with the Android NDK
#   android-binary.sh check BINARY                    check the result's ELF headers
#
# The binary is GOOS=android GOARCH=arm64 with cgo, linked against bionic by the
# NDK's clang. Termux's loader (/system/bin/linker64) maps only position
# independent executables, and the Android resolver and certificate store need
# bionic, so a static or CGO_ENABLED=0 binary does not qualify.
#
# Environment:
#   ANDROID_NDK_LATEST_HOME, ANDROID_NDK_HOME   NDK root (GitHub's ubuntu runners set both)
#   PIG_ANDROID_API                             minimum Android API level (default 24, Termux's)
set -euo pipefail

api=${PIG_ANDROID_API:-24}
host_tag=linux-x86_64

ndk_root() {
  local ndk=${ANDROID_NDK_LATEST_HOME:-${ANDROID_NDK_HOME:-}}
  if [ -z "$ndk" ] || [ ! -d "$ndk/toolchains/llvm/prebuilt/$host_tag" ]; then
    echo "android-binary: set ANDROID_NDK_LATEST_HOME or ANDROID_NDK_HOME to an NDK for $host_tag" >&2
    exit 1
  fi
  echo "$ndk"
}

build() {
  local ndk cc revision
  ndk=$(ndk_root)
  cc="$ndk/toolchains/llvm/prebuilt/$host_tag/bin/aarch64-linux-android${api}-clang"
  [ -x "$cc" ] || { echo "android-binary: $cc not found" >&2; exit 1; }
  revision=$(sed -n 's/^Pkg.Revision *= *//p' "$ndk/source.properties")
  echo "android-binary: NDK $revision, API $api" >&2
  CGO_ENABLED=1 GOOS=android GOARCH=arm64 CC="$cc" go build "$@"
}

# The shared libraries bionic provides to every app; none else exists on Android.
allowed_libraries='libc.so libdl.so libm.so liblog.so'

check() {
  local binary=$1 header program dynamic needed library
  header=$(readelf -h "$binary")
  grep -Eq 'Machine: +AArch64' <<<"$header" || { echo "android-binary: $binary is not AArch64" >&2; exit 1; }
  # e_type DYN is a PIE; linker64 rejects ET_EXEC with "unexpected e_type: 2".
  grep -Eq 'Type: +DYN' <<<"$header" || { echo "android-binary: $binary is not a position independent executable" >&2; exit 1; }
  program=$(readelf -l "$binary")
  grep -q 'Requesting program interpreter: /system/bin/linker64' <<<"$program" ||
    { echo "android-binary: $binary does not request /system/bin/linker64" >&2; exit 1; }
  dynamic=$(readelf -d "$binary")
  needed=$(sed -n 's/.*(NEEDED).*Shared library: \[\(.*\)\]/\1/p' <<<"$dynamic")
  grep -qx 'libc.so' <<<"$needed" || { echo "android-binary: $binary does not link bionic's libc (built without cgo?)" >&2; exit 1; }
  for library in $needed; do
    case " $allowed_libraries " in
      *" $library "*) ;;
      *) echo "android-binary: $binary needs $library, which Android does not provide" >&2; exit 1 ;;
    esac
  done
  echo "android-binary: $binary ok (needs: $(tr '\n' ' ' <<<"$needed" | sed 's/ $//'))"
}

case "${1:-}" in
  build) shift; build "$@" ;;
  check) [ $# -eq 2 ] || { echo "usage: $0 check BINARY" >&2; exit 2; }; check "$2" ;;
  *) echo "usage: $0 build [go build args...] | check BINARY" >&2; exit 2 ;;
esac
