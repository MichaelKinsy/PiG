#!/bin/sh
# Check out the reference the corpus is captured from (PIN) into the cache and install its dependencies. Idempotent.
#   DURABLE_CONTRACT_CACHE   cache directory (default durable/contract/.cache)
#   CONTRACT_PI_SOURCE       use an existing checkout instead (must be at the pinned commit)
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
cache=${DURABLE_CONTRACT_CACHE:-$here/.cache}
repo=$(awk '$1 == "repo" { print $2 }' "$here/PIN")
commit=$(awk '$1 == "main" { print $2 }' "$here/PIN")
dir=${CONTRACT_PI_SOURCE:-$cache/pi-main}
mkdir -p "$cache"
if [ ! -d "$dir/.git" ]; then
	git clone --quiet --filter=blob:none --no-checkout "$repo" "$dir"
fi
if [ "$(git -C "$dir" rev-parse HEAD 2>/dev/null || true)" != "$commit" ]; then
	git -C "$dir" fetch --quiet origin "$commit" 2>/dev/null || true
	git -C "$dir" checkout --quiet "$commit"
fi
if [ ! -d "$dir/node_modules" ]; then
	(cd "$dir" && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error)
fi
# The model catalogs (packages/ai/src/providers/data) are generated at the reference's build time and not in git. The
# catalogs of the published 1.0.4 package are the same data the examples' provider modules import; behaviour does not read them.
if [ ! -d "$dir/packages/ai/src/providers/data" ]; then
	tmp=$(mktemp -d)
	(cd "$tmp" && npm pack --silent @earendil-works/pi-ai@1.0.4 >/dev/null && tar xzf ./*.tgz)
	cp -r "$tmp/package/dist/providers/data" "$dir/packages/ai/src/providers/data"
	rm -rf "$tmp"
fi
# The release the corpus keeps readable (PIN release): a worktree of the same clone with the reference's dependencies shared.
rel=$(awk '$1 == "release" { print $2 }' "$here/PIN")
old=${CONTRACT_PI_104_SOURCE:-$cache/pi-1.0.4}
if [ ! -d "$old" ]; then
	git -C "$dir" fetch --quiet origin "refs/tags/$rel:refs/tags/$rel" 2>/dev/null || true
	git -C "$dir" worktree add --force --detach "$old" "$rel" >/dev/null
fi
for pkg in ai coding-agent durable; do
	[ -e "$old/packages/$pkg/node_modules" ] || ln -s "$dir/packages/$pkg/node_modules" "$old/packages/$pkg/node_modules"
done
[ -e "$old/node_modules" ] || ln -s "$dir/node_modules" "$old/node_modules"
[ -d "$old/packages/ai/src/providers/data" ] || cp -r "$dir/packages/ai/src/providers/data" "$old/packages/ai/src/providers/data"
echo "$dir"
