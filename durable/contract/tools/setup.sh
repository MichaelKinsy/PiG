#!/bin/sh
# Check out the references the corpus is captured from (PIN: `main` for the rows, `ticks` for the tick rows) into the cache and
# install their dependencies. Idempotent.
#   DURABLE_CONTRACT_CACHE   cache directory (default durable/contract/.cache)
#   CONTRACT_PI_SOURCE       use an existing checkout instead (must be at the pinned `main` commit)
#   CONTRACT_PI_TICKS_SOURCE the same for the `ticks` commit
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
# The dependency tree follows the reference's lock file: a cache installed for an earlier pin keeps its node_modules across the
# checkout above, so the tree is reinstalled whenever the lock file differs from the one it was installed from.
install_deps() {
	lock=$( (sha256sum "$1/package-lock.json" 2>/dev/null || shasum -a 256 "$1/package-lock.json") | cut -d' ' -f1)
	if [ ! -d "$1/node_modules" ] || [ "$(cat "$1/node_modules/.contract-lock" 2>/dev/null || true)" != "$lock" ]; then
		(cd "$1" && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error)
		echo "$lock" >"$1/node_modules/.contract-lock"
	fi
	# The model catalogs (packages/ai/src/providers/data) are generated at the reference's build time and not in git. The
	# catalogs of the published 1.0.4 package are the same data the examples' provider modules import; behaviour does not read them.
	if [ ! -d "$1/packages/ai/src/providers/data" ]; then
		tmp=$(mktemp -d)
		(cd "$tmp" && npm pack --silent @earendil-works/pi-ai@1.0.4 >/dev/null && tar xzf ./*.tgz)
		cp -r "$tmp/package/dist/providers/data" "$1/packages/ai/src/providers/data"
		rm -rf "$tmp"
	fi
}
install_deps "$dir"
# The reference of the tick rows (PIN `ticks`): a worktree of the same clone at its own commit, with its own dependencies.
ticks=$(awk '$1 == "ticks" { print $2 }' "$here/PIN")
tdir=${CONTRACT_PI_TICKS_SOURCE:-$cache/pi-ticks}
if [ ! -d "$tdir" ]; then
	git -C "$dir" fetch --quiet origin "$ticks" 2>/dev/null || true
	git -C "$dir" worktree add --force --detach "$tdir" "$ticks" >/dev/null
fi
if [ "$(git -C "$tdir" rev-parse HEAD 2>/dev/null || true)" != "$ticks" ]; then
	git -C "$tdir" fetch --quiet origin "$ticks" 2>/dev/null || true
	git -C "$tdir" checkout --quiet "$ticks"
fi
install_deps "$tdir"
# The release the corpus keeps readable (PIN release): a worktree of the same clone with the reference's dependencies shared.
rel=$(awk '$1 == "release" { print $2 }' "$here/PIN")
old=${CONTRACT_PI_104_SOURCE:-$cache/pi-1.0.4}
if [ ! -d "$old" ]; then
	git -C "$dir" fetch --quiet origin "refs/tags/$rel:refs/tags/$rel" 2>/dev/null || true
	git -C "$dir" worktree add --force --detach "$old" "$rel" >/dev/null
fi
for pkg in ai coding-agent durable; do
	ln -sfn "$dir/packages/$pkg/node_modules" "$old/packages/$pkg/node_modules"
done
ln -sfn "$dir/node_modules" "$old/node_modules"
[ -d "$old/packages/ai/src/providers/data" ] || cp -r "$dir/packages/ai/src/providers/data" "$old/packages/ai/src/providers/data"
echo "$dir"
