#!/usr/bin/env bash
# wt-provision <worktree> [--oracle-build <dir>]: make a PiG worktree Pi-oracle-ready without copying.
#   - .upstream/current -> shared read-only mirror of the pinned Pi source (default ~/.cache/pig-dev/upstream/v<pin>)
#   - extensions/sdk-ts/node_modules -> the source-built Pi oracle `make parity-deps` links (automation/pin/pin.py install-oracle): the
#     shared build store's build for the pinned commit (~/.cache/pig-dev/pi-builds/<commit>), built there first when it is missing. Its
#     src/ install carries the build tools the bundle-regeneration tests use (esbuild). --oracle-build <dir> links a given build instead.
#   - other tracked lockfiles (durable/interop, testdata trees) -> host-shared locked npm trees (PIG_NODE_MODULES_STORE, via npm-locked.py)
# Only adds symlinks/files (a real extensions/sdk-ts/node_modules, e.g. one an APFS clone copied, is replaced by the link); idempotent;
# exits non-zero if the pinned oracle is missing or mismatched.
# Env: PIG_UPSTREAM_MIRRORS (default ~/.cache/pig-dev/upstream), PIG_NODE_MODULES_STORE (default ~/.cache/pig-node-modules), PIG_PI_BUILD_STORE
# and PIG_DEV_HOME (as pin.py install-oracle reads them)
set -euo pipefail
usage="usage: wt-provision <worktree> [--oracle-build <dir>]"
wt=; oracle_build=
while [ $# -gt 0 ]; do
  case $1 in
    --oracle-build) oracle_build=${2:?$usage}; shift 2 ;;
    -*) echo "$usage" >&2; exit 2 ;;
    *) [ -z "$wt" ] || { echo "$usage" >&2; exit 2; }; wt=$1; shift ;;
  esac
done
[ -n "$wt" ] || { echo "$usage" >&2; exit 2; }
wt=$(cd "$wt" && pwd -P)
mirrors=${PIG_UPSTREAM_MIRRORS:-$HOME/.cache/pig-dev/upstream}
export PIG_NODE_MODULES_STORE=${PIG_NODE_MODULES_STORE:-$HOME/.cache/pig-node-modules}
die() { echo "wt-provision: FAIL: $*" >&2; exit 1; }

pin=$wt/.upstream/pin
[ -f "$pin" ] || { [ -L "$wt/.upstream" ] && git -C "$wt" cat-file -e HEAD:.upstream/pin 2>/dev/null; } || [ -f "$pin" ] || die "no .upstream/pin in $wt"
if [ -L "$wt/.upstream" ]; then
  # A whole-directory symlink (e.g. to the main clone's stale mirror) hides the tracked pin; replace the link with a real dir.
  rm "$wt/.upstream"; mkdir "$wt/.upstream"
  git -C "$wt" show HEAD:.upstream/pin > "$wt/.upstream/pin"
fi
get() { sed -n "s/^$1=//p" "$wt/.upstream/pin" | head -1; }
ref=$(get ref); commit=$(get commit); pkgver=$(get package_version); version=$(get version)
if [ -z "$ref" ] || [ -z "$commit" ] || [ -z "$pkgver" ] || [ -z "$version" ]; then die "incomplete pin"; fi
# Same naming as automation/pin/pinlib.py: mirror dir is "v"+version (a commit pin's version is <pkg>+<commit[:8]>).
mirror=v$version
oracle_node_modules=
if [ -n "$oracle_build" ]; then
  stamp=$oracle_build/.pig-pi-oracle.json
  [ -f "$stamp" ] || stamp=$oracle_build/oracle/.pig-pi-oracle.json
  [ -f "$stamp" ] || die "no .pig-pi-oracle.json stamp under $oracle_build"
  grep -q "\"commit\": *\"$commit\"" "$stamp" || die "oracle build stamp $stamp is not commit $commit"
  grep -q '"source": *"build"' "$stamp" || die "oracle build stamp $stamp is not a source build"
  oracle_node_modules=$(cd "$(dirname "$stamp")" && pwd -P)/node_modules
  [ -d "$oracle_node_modules" ] || die "no node_modules next to $stamp"
fi

# 1. Source mirror: a mirror dir whose marker carries the pinned commit.
src=
cand=$mirrors/$mirror
m=$cand/.pig-upstream-source.json
if [ -f "$m" ] && grep -q "\"commit\": \"$commit\"" "$m"; then src=$(cd "$cand" && pwd -P); fi
[ -n "$src" ] || die "no mirror $mirror for $ref@$commit under $mirrors (run make upstream-mirror in a checkout, or set PIG_UPSTREAM_MIRRORS)"
for link in current "$mirror"; do
  t=$wt/.upstream/$link
  if [ -L "$t" ] && [ "$(readlink "$t")" = "$src" ]; then continue; fi
  if [ -L "$t" ]; then
    rm "$t"
  elif [ -e "$t" ]; then
    die "$t exists and is not a symlink"
  fi
  ln -s "$src" "$t"
done

# 2. node_modules: the source-built Pi oracle (the same tree `make parity-deps` links), pinned Pi package version verified.
sdk=$wt/extensions/sdk-ts
[ -f "$sdk/package-lock.json" ] || die "no $sdk/package-lock.json"
if [ -L "$sdk/node_modules" ] && [ ! -e "$sdk/node_modules" ]; then rm "$sdk/node_modules"; fi
if [ -n "$oracle_node_modules" ]; then
  if [ -L "$sdk/node_modules" ] && [ "$(readlink "$sdk/node_modules")" = "$oracle_node_modules" ]; then :
  else
    # A real directory here (an APFS clone copies the source checkout's install) is replaced, never used.
    rm -rf "$sdk/node_modules"; ln -s "$oracle_node_modules" "$sdk/node_modules"
  fi
else
  # pin.py install-oracle links the shared build store's build of the pinned commit (PIG_PI_BUILD_STORE, else
  # ${PIG_DEV_HOME:-~/.cache/pig-dev}/pi-builds/<commit>), building it there first when it is missing, and replaces whatever
  # extensions/sdk-ts/node_modules was. It also points .upstream/current at the pin's mirror dir, which step 1 linked.
  (cd "$wt" && env -u PIG_PARITY_PI_PUBLISHED python3 automation/pin/pin.py install-oracle) >&2 || die "pin.py install-oracle failed in $wt"
  [ -L "$sdk/node_modules" ] || die "pin.py install-oracle left no link at $sdk/node_modules"
  oracle_node_modules=$(readlink "$sdk/node_modules")
fi
stamp=$(dirname "$oracle_node_modules")/.pig-pi-oracle.json
if ! grep -qs "\"commit\": *\"$commit\"" "$stamp" || ! grep -qs '"source": *"build"' "$stamp"; then
  die "$oracle_node_modules is not the source-built Pi oracle of $commit (stamp $stamp)"
fi
# Every tracked lockfile an oracle test installs from: sdk-ts, durable/interop, and testdata trees (ai/testdata/sdk, protocol typebox...).
dirs=("$sdk")
while IFS= read -r lock; do dirs+=("$wt/$(dirname "$lock")"); done < <(git -C "$wt" ls-files | grep -E '(^durable/interop|/testdata/([^/]+/)*[^/]+)/package-lock\.json$|^durable/interop/package-lock\.json$' | sort -u)
for dir in "${dirs[@]}"; do
  [ -f "$dir/package-lock.json" ] || continue
  if [ -d "$dir/node_modules" ] && [ ! -L "$dir/node_modules" ] && [ ! -f "$dir/node_modules/.pig-deps-ready" ]; then die "$dir/node_modules is a real unstamped dir; refusing to touch it"; fi
  [ -n "$oracle_node_modules" ] && [ "$dir" = "$sdk" ] && continue
  npm_config_prefer_offline=true python3 "$wt/automation/ci/npm-locked.py" "$dir" || die "locked npm install failed in $dir"
done

# 3. Prove it.
pkg=$sdk/node_modules/@earendil-works/pi-coding-agent
got=$(sed -n 's/.*"version": "\(.*\)".*/\1/p' "$pkg/package.json" | head -1)
[ "$got" = "$pkgver" ] || die "Pi oracle version $got != pin $pkgver"
[ -x "$sdk/node_modules/.bin/pi" ] || die "missing $sdk/node_modules/.bin/pi"
[ -f "$wt/.upstream/current/packages/coding-agent/package.json" ] || die ".upstream/current incomplete"
echo "wt-provision: run Pi-oracle tests with COLORTERM=truecolor (the CI image sets it; Pi picks theme colors from it)" >&2
echo "wt-provision: ok $wt (Pi $got, mirror $src)"
