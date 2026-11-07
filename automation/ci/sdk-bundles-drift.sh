#!/bin/sh
# SPDX-License-Identifier: MIT
# Fail when a packed SDK bundle (the sdk.zst and bundle_pack.go of internal/gosdkbundle, extensions/sdk-lisp, sdk-py and sdk-rs, and
# the nested extensions/sdk module's .gz copies and bundle_embed.go) differs from what `make sdk-bundles` writes from the SDK
# sources. The generators keep a frame or copy that already holds the sources' text, so the comparison is of the text as well as of
# the bytes. They write in place, so this runs them, compares against a copy of the five directories taken first, and puts the
# committed bytes back.
set -eu
dirs="extensions/sdk internal/gosdkbundle extensions/sdk-lisp extensions/sdk-py extensions/sdk-rs"
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
