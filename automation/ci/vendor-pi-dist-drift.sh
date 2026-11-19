#!/bin/sh
# SPDX-License-Identifier: MIT
# Fail when the vendored Pi runtime (coding/extension/host/subprocess/runtime-node/shims: pi-dist, the third-party copies and
# vendor-manifest.json) differs from what `make vendor-pi-dist` writes from the installed oracle of the pinned Pi. The oracle carries the
# post-release backports (automation/gen/pi-post-release-backports.mjs), so a backport added without vendoring leaves the shims stale
# while every other generated file still matches: this is the gate that sees it. make generate vendors before it packs the archive.
# The generator runs on a copy (PIG_VENDOR_SHIMS), so the committed tree is never touched. Run it through make: the target installs the
# oracle first (make parity-deps), and the oracle is rebuilt when the backport list or the pin changed.
# The archive (runtime-node.pack, runtime_node_digest_generated.go) follows the shims: make node-runtime-drift checks it.
set -eu
root=$(cd -- "$(dirname -- "$0")/../.." && pwd)
shims=$root/coding/extension/host/subprocess/runtime-node/shims
tmp=$(mktemp -d "${TMPDIR:-/tmp}/pig.XXXXXXXX")
# The oracle builds are read-only and cp keeps their modes.
trap 'chmod -R u+w "$tmp" 2>/dev/null || true; rm -rf "$tmp"' EXIT
cp -R "$shims" "$tmp/shims"
if ! PIG_VENDOR_SHIMS="$tmp/shims" "$root/automation/gen/vendor-pi-dist.sh" >"$tmp/vendor.log" 2>&1; then
    cat "$tmp/vendor.log" >&2
    echo "vendor-pi-dist-drift: make vendor-pi-dist fails on this tree (see above)" >&2
    exit 1
fi
# Contents, then the executable bits git records.
modes() { (cd "$1" && find . -type f -perm -u+x | sort); }
modes "$shims" >"$tmp/modes.committed"
modes "$tmp/shims" >"$tmp/modes.vendored"
status=0
diff -rq "$shims" "$tmp/shims" >"$tmp/diff" || status=1
diff -u "$tmp/modes.committed" "$tmp/modes.vendored" >>"$tmp/diff" || status=1
if [ "$status" -eq 0 ]; then
    exit 0
fi
sed -e "s#$tmp/shims#vendored#g" -e "s#$shims#committed#g" "$tmp/diff" | head -n 60 >&2
echo "the vendored Pi runtime (runtime-node/shims) is stale: make vendor-pi-dist writes the files listed above" >&2
echo "run: make generate (or: make vendor-pi-dist, then make node-runtime), then commit the result as its own chore(generated) commit" >&2
exit 1
