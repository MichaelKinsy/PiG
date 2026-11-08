#!/usr/bin/env bash
# SPDX-License-Identifier: MIT
# Differential fuzz of the Piglet's rewrite (piglets/pig-bolt/extensions/boltruntime/rewrite.go) against the expected rewrites the generator writes and against
# Pi-Bolt's rewriteCopy. Run it where Go and a Bun are (the CI container):
#   BUN=/path/to/bun OUT=$TMPDIR/fuzz [OLD_TREE=/path/to/a/checkout/with/the/previous/rewrite] [MODULES=4050] [SEED=1] bench/bolt-aot/rewrite-exts/fuzz/run.sh
# It downloads Pi-Bolt's gen-plugin-manifest.mjs at commit 1098b1ce19cf67daf8ab0995d7ef7e2a2bea8681 (the bolt-v1.1.2 release) unless PIBOLT_GPM names a copy. OLD_TREE, if
# set, must hold a rewrite.go (any earlier version) and gets rewrite_fuzz_test.go copied in; its outputs are the "old" column of the report.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../../.." && pwd)"
: "${BUN:?set BUN to a Bun executable}"
: "${OUT:?set OUT to a scratch directory}"
mkdir -p "$OUT"
gpm="${PIBOLT_GPM:-$OUT/gen-plugin-manifest.mjs}"
if [ ! -f "$gpm" ]; then
	curl -sfL -o "$gpm" https://raw.githubusercontent.com/opensec-git/Pi-Bolt/1098b1ce19cf67daf8ab0995d7ef7e2a2bea8681/packages/coding-agent/src/gen-plugin-manifest.mjs
fi
"$BUN" "$here/gen.mjs" "$OUT/corpus.json" "${MODULES:-4050}" "${SEED:-1}"
pkg="piglets/pig-bolt"
(cd "$root/$pkg" && GOWORK=off PIG_AOT_FUZZ_HAND_OUT="$OUT/hand.json" go test ./extensions/boltruntime -run '^TestRewriteFuzzHandCases$' -count=1 >/dev/null)
# The Go side reads the corpus and the hand cases as one list, in that order.
"$BUN" -e 'const [c, h, o] = process.argv.slice(1); const fs = require("node:fs"); fs.writeFileSync(o, JSON.stringify(JSON.parse(fs.readFileSync(c)).concat(JSON.parse(fs.readFileSync(h)))))' "$OUT/corpus.json" "$OUT/hand.json" "$OUT/all.json"
(cd "$root/$pkg" && GOWORK=off PIG_AOT_FUZZ_CORPUS="$OUT/all.json" PIG_AOT_FUZZ_OUT="$OUT/ours.json" go test ./extensions/boltruntime -run '^TestRewriteFuzzCorpus$' -count=1 >/dev/null)
if [ -n "${OLD_TREE:-}" ]; then
	cp "$root/$pkg/extensions/boltruntime/rewrite_fuzz_test.go" "$OLD_TREE/$pkg/extensions/boltruntime/"
	(cd "$OLD_TREE/$pkg" && GOWORK=off PIG_AOT_FUZZ_CORPUS="$OUT/all.json" PIG_AOT_FUZZ_OUT="$OUT/old.json" go test ./extensions/boltruntime -run '^TestRewriteFuzzCorpus$' -count=1 >/dev/null)
	rm -f "$OLD_TREE/$pkg/extensions/boltruntime/rewrite_fuzz_test.go"
else
	cp "$OUT/ours.json" "$OUT/old.json"
fi
"$BUN" "$here/compare.mjs" "$OUT/corpus.json" "$OUT/hand.json" "$OUT/ours.json" "$OUT/old.json" "$gpm" --repros "${REPROS:-14}"
