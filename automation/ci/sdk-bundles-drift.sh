#!/bin/sh
# SPDX-License-Identifier: MIT
# Fail when a packed SDK bundle (each SDK's sdk.zst and bundle_pack.go) differs from what `make sdk-bundles` writes from the SDK
# sources. The generator keeps a frame that already decodes to the sources' files, so the comparison is of the files the frame
# holds as well as of the bytes. It writes in place, so this runs it, compares against a copy of the four SDK directories taken
# first, and puts the committed bytes back.
set -eu
dirs="extensions/sdk extensions/sdk-lisp extensions/sdk-py extensions/sdk-rs"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# shellcheck disable=SC2086
tar -cf "$tmp/before.tar" $dirs
make --no-print-directory sdk-bundles
# shellcheck disable=SC2086
tar -cf "$tmp/after.tar" $dirs
mkdir "$tmp/before" "$tmp/after"
tar -xf "$tmp/before.tar" -C "$tmp/before"
tar -xf "$tmp/after.tar" -C "$tmp/after"
# compare contents, not tar bytes: the generators rewrite some files with identical contents and a new mtime
if diff -rq "$tmp/before" "$tmp/after" >"$tmp/diff"; then
    exit 0
fi
sed -e "s#$tmp/##g" "$tmp/diff" >&2
# restore the committed bytes: the check must not leave the tree rewritten
# shellcheck disable=SC2086
rm -rf $dirs
tar -xf "$tmp/before.tar"
echo "packed SDK bundles are stale (listed above)" >&2
echo "run: make generate (or: make sdk-bundles), then commit the result" >&2
exit 1
