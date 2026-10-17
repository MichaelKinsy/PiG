#!/bin/sh
# Builds the valgrind release the count gate's native instruction counts (`ir`, cli/counts.mjs) were measured with,
# in user space under PREFIX (no root, no system package):
#
#   sh durable/contract/tools/build-valgrind.sh PREFIX
#   VALGRIND=PREFIX/bin/valgrind node durable/contract/cli/counts.mjs check
#
# The release tarball must match the pinned sha256. Its upstream signature (valgrind-3.27.1.tar.bz2.asc) is by
# Mark J. Wielaard, key 28FF4B30A88D5523A7E2CBA20376A15C6FFC11CD.
set -eu
VERSION=3.27.1
SHA256=5d589152eb8071c02feab8ce6ab719e431a1fbc3e2b1700f5432632a8b9264dc
PREFIX=${1:?usage: build-valgrind.sh PREFIX}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -fsSL -o "$work/valgrind.tar.bz2" "https://sourceware.org/pub/valgrind/valgrind-$VERSION.tar.bz2"
echo "$SHA256  $work/valgrind.tar.bz2" | sha256sum -c -
tar xjf "$work/valgrind.tar.bz2" -C "$work"
cd "$work/valgrind-$VERSION"
./configure --prefix="$PREFIX" > "$work/configure.log"
make -j"${JOBS:-2}" > "$work/make.log"
make install > "$work/install.log"
"$PREFIX/bin/valgrind" --version
