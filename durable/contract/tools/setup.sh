#!/bin/sh
# Check out the references the corpus is captured from (PIN: `main` for the rows, `release` for the R rows) into the cache and
# install their dependencies. Idempotent.
#   DURABLE_CONTRACT_CACHE   cache directory (default durable/contract/.cache)
#   CONTRACT_PI_SOURCE       use an existing checkout instead (must be at the pinned `main` commit)
#   CONTRACT_PI_PACKS_DIR    vendored pi-packs directory (default third_party/pi-packs)
set -eu
here=$(cd "$(dirname "$0")/.." && pwd)
cache=${DURABLE_CONTRACT_CACHE:-$here/.cache}
repo=$(awk '$1 == "repo" { print $2 }' "$here/PIN")
commit=$(awk '$1 == "main" { print $2 }' "$here/PIN")
packs_dir=${CONTRACT_PI_PACKS_DIR:-$here/../../third_party/pi-packs}
dir=${CONTRACT_PI_SOURCE:-$cache/pi-main}
mkdir -p "$cache"
if [ ! -d "$dir/.git" ]; then
	git clone --quiet --filter=blob:none --no-checkout "$repo" "$dir"
fi
if [ "$(git -C "$dir" rev-parse HEAD 2>/dev/null || true)" != "$commit" ]; then
	git -C "$dir" fetch --quiet origin "$commit" 2>/dev/null || true
	git -C "$dir" checkout --quiet "$commit"
fi
# Resolve the vendored @earendil-works/pi-ai tarball whose manifest commit matches PIN main.
# Fails loudly if the manifest is missing, mismatches the PIN commit, or names no @earendil-works/pi-ai tarball.
resolve_pi_ai_pack() {
	manifest=$packs_dir/manifest.json
	[ -f "$manifest" ] || { echo "setup.sh: missing vendored pi-packs manifest: $manifest (expected commit $commit)" >&2; exit 1; }
	pack_name=$(node - "$manifest" "$commit" <<'NODE'
const fs = require("node:fs");
const [manifestPath, wantCommit] = process.argv.slice(2);
const m = JSON.parse(fs.readFileSync(manifestPath, "utf8"));
if (m.commit !== wantCommit) {
	console.error(`setup.sh: ${manifestPath} commit is ${m.commit ?? "<missing>"}, durable/contract/PIN main is ${wantCommit}; run make vendor-pi-packs`);
	process.exit(1);
}
const matches = Object.entries(m.packs || {}).filter(([, info]) => info && info.name === "@earendil-works/pi-ai").map(([file]) => file);
if (matches.length !== 1) {
	console.error(`setup.sh: ${manifestPath} must list exactly one @earendil-works/pi-ai pack, found ${matches.length}`);
	process.exit(1);
}
process.stdout.write(matches[0]);
NODE
	)
	pack_path=$packs_dir/$pack_name
	[ -f "$pack_path" ] || { echo "setup.sh: vendored @earendil-works/pi-ai pack not found: $pack_path" >&2; exit 1; }
	printf "%s" "$pack_path"
}
# Check every ./data/<file> imported by packages/ai/src/providers/*.ts is present in packages/ai/src/providers/data.
verify_provider_data() {
	node - "$1" "$2" <<'NODE'
const fs = require("node:fs");
const path = require("node:path");
const [root, source] = process.argv.slice(2);
const providersDir = path.join(root, "packages", "ai", "src", "providers");
if (!fs.existsSync(providersDir)) process.exit(0);
const dataDir = path.join(providersDir, "data");
const needed = new Set();
for (const entry of fs.readdirSync(providersDir)) {
	if (!entry.endsWith(".ts")) continue;
	const text = fs.readFileSync(path.join(providersDir, entry), "utf8");
	for (const match of text.matchAll(/["']\.\/data\/([^"'/]+)["']/g)) needed.add(match[1]);
}
const missing = [...needed].sort().filter((f) => !fs.existsSync(path.join(dataDir, f)));
if (missing.length > 0) {
	console.error(`setup.sh: ${dataDir} is missing imported provider catalog(s): ${missing.join(", ")} (source: ${source})`);
	process.exit(1);
}
NODE
}
# Sync the generated model catalogs (packages/ai/src/providers/data) from the committed vendored pi-ai pack of PIN main.
# Runs whenever the stamp differs from the pack digest or any imported catalog is missing, so an incomplete restored cache repairs itself.
sync_provider_data() {
	target=$1/packages/ai/src/providers/data
	pack=$2
	stamp=$3
	need_sync=0
	if [ ! -d "$target" ] || [ "$(cat "$target/.contract-pack" 2>/dev/null || true)" != "$stamp" ]; then
		need_sync=1
	elif ! verify_provider_data "$1" "$pack" 2>/dev/null; then
		need_sync=1
	fi
	if [ "$need_sync" = 1 ]; then
		tmp=$(mktemp -d)
		tar xzf "$pack" -C "$tmp" package/dist/providers/data
		if [ ! -d "$tmp/package/dist/providers/data" ]; then
			rm -rf "$tmp"
			echo "setup.sh: $pack lacks package/dist/providers/data" >&2
			exit 1
		fi
		rm -rf "$target"
		mkdir -p "$(dirname "$target")"
		cp -r "$tmp/package/dist/providers/data" "$target"
		printf "%s\n" "$stamp" >"$target/.contract-pack"
		rm -rf "$tmp"
	fi
	verify_provider_data "$1" "$pack"
}
pi_ai_pack=$(resolve_pi_ai_pack)
pi_ai_stamp=$( (sha256sum "$pi_ai_pack" 2>/dev/null || shasum -a 256 "$pi_ai_pack") | cut -d" " -f1)
# The dependency tree follows the reference's lock file: a cache installed for an earlier pin keeps its node_modules across the
# checkout above, so the tree is reinstalled whenever the lock file differs from the one it was installed from.
install_deps() {
	lock=$( (sha256sum "$1/package-lock.json" 2>/dev/null || shasum -a 256 "$1/package-lock.json") | cut -d" " -f1)
	if [ ! -d "$1/node_modules" ] || [ "$(cat "$1/node_modules/.contract-lock" 2>/dev/null || true)" != "$lock" ]; then
		(cd "$1" && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error)
		echo "$lock" >"$1/node_modules/.contract-lock"
	fi
	sync_provider_data "$1" "$pi_ai_pack" "$pi_ai_stamp"
}
install_deps "$dir"
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
# The release's dependencies are the reference's except its own Chord: Pi main made Chord v2 the package root (e9c0ff4d), and the release's
# Durable imports Chord v1's root (copyJson, Context, ...). A directory of links to the reference's entries stands in for the shared
# node_modules, with @earendil-works/chord linked to the release's package.
rm -rf "$old/node_modules"
mkdir -p "$old/node_modules/@earendil-works"
for entry in "$dir"/node_modules/* "$dir"/node_modules/.bin; do
	[ "$(basename "$entry")" = "@earendil-works" ] || ln -sfn "$entry" "$old/node_modules/$(basename "$entry")"
done
for entry in "$dir"/node_modules/@earendil-works/*; do
	ln -sfn "$entry" "$old/node_modules/@earendil-works/$(basename "$entry")"
done
ln -sfn "$old/packages/chord" "$old/node_modules/@earendil-works/chord"
sync_provider_data "$old" "$pi_ai_pack" "$pi_ai_stamp"
echo "$dir"
