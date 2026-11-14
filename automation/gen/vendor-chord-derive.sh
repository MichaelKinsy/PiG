#!/usr/bin/env bash
# Make the derived Pi tree a vendored chord copy is taken from: a detached worktree of a Pi clone at <commit> with the
# carried commits cherry-picked onto it, in order (PI_TREE of automation/gen/vendor-chord-common.sh).
#   vendor-chord-derive.sh <pi repo> <new worktree dir> <commit> [<carried commits, comma-separated>]
# A carried commit is a whole Pi commit, and a Pi commit often changes apps/ and other packages beside chord. The vendoring
# scripts read these roots of the tree and nothing else (vendor-chord-plugins-node.mjs, vendor-chord-surfaces-client.mjs):
#   packages/chord  packages/schema  packages/telemetry  package-lock.json
# A conflict in a path under a root fails: the copy would not be Pi's. A conflict in a path outside them leaves that path at
# the derived tree's side (as it was before the pick; a file the tree does not have stays absent) and takes the rest of the
# commit: no vendored byte is read from there, and carrying those files' history (apps/p2 moves daily) would carry Pi's
# head. Each path left is printed, one `left <commit> <path>` line on standard output, so nothing is left silently: the
# sync ledger of the carried commit names them.
# The caller removes the worktree (`git -C <pi repo> worktree remove --force <dir>`).
set -euo pipefail

usage="usage: vendor-chord-derive.sh PI_REPO NEW_WORKTREE_DIR COMMIT [CARRIED]"
repo=${1:?"$usage"}
dir=${2:?"$usage"}
commit=${3:?"$usage"}
carried=${4:-}
roots='^(packages/(chord|schema|telemetry)/|package-lock\.json$)'

git -C "$repo" worktree add --quiet --detach "$dir" "$commit"
pick=(git -C "$dir" -c user.name=vendor-chord-derive -c user.email=vendor@localhost -c commit.gpgsign=false -c rerere.enabled=false)
for id in ${carried//,/ }; do
	if "${pick[@]}" cherry-pick --allow-empty "$id" >/dev/null 2>&1; then
		continue
	fi
	conflicted=$(git -C "$dir" diff --name-only --diff-filter=U)
	inside=$(printf '%s\n' "$conflicted" | grep -E "$roots" || true)
	if [ -z "$conflicted" ] || [ -n "$inside" ]; then
		echo "vendor-chord-derive: Pi $id does not cherry-pick onto $commit and the commits carried before it${inside:+: it conflicts in $(printf '%s' "$inside" | tr '\n' ' ')}" >&2
		exit 1
	fi
	while IFS= read -r path; do
		if git -C "$dir" cat-file -e "HEAD:$path" 2>/dev/null; then
			git -C "$dir" restore --source=HEAD --staged --worktree -- "$path"
		else
			git -C "$dir" rm --quiet --force -- "$path"
		fi
		echo "left $id $path"
	done <<<"$conflicted"
	# Pi's own commit hooks are not run for a derived tree: a pick without a conflict runs none either.
	"${pick[@]}" -c core.hooksPath=/dev/null commit --quiet --allow-empty --no-edit >/dev/null
done
