#!/usr/bin/env bash
# Prove that both vendored chord trees (chord/nextnode/plugins/node and chord/nextnode/surfaces/client) are what their
# manifests record: Pi at the manifest's commit with exactly its `carried` commits cherry-picked onto it, in order. It
# makes that tree in a scratch worktree of a Pi clone (automation/gen/vendor-chord-derive.sh: a carried commit's conflict
# outside the roots the copies read leaves that path as it was and is printed; one inside them fails), runs each tree's
# vendoring script into a scratch directory, and
# compares the manifest it writes (commit, pin, carried, patches, versions, every file's hash) with the committed one, byte for
# byte. The Go tests TestNodeVendoredFilesAreExactlyTheManifests and TestClientVendoredFilesAreExactlyTheManifests prove
# the other half: the committed files are the ones the committed manifests hash.
#   PI_REPO        a clone of Pi that has every manifest's commit and carried commits (required)
#   PI_TELEMETRY   as automation/gen/vendor-chord-common.sh
set -euo pipefail

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
repo=${PI_REPO:?vendor-chord-carried-check: set PI_REPO to a clone of Pi}
work=$(mktemp -d "${TMPDIR:-/tmp}/vendor-chord-carried.XXXXXX")
drop_tree() {
	if [ -d "$work/tree" ]; then
		git -C "$repo" worktree remove --force "$work/tree"
	fi
}
trap 'drop_tree; rm -rf "$work"' EXIT

status=0
made=
for name in plugins-node surfaces-client; do
	case $name in
	plugins-node) dir=chord/nextnode/plugins/node ;;
	surfaces-client) dir=chord/nextnode/surfaces/client ;;
	esac
	manifest="$root/$dir/manifest.json"
	commit=$(node -p "require('$manifest').commit")
	carried=$(node -p "(require('$manifest').carried ?? []).join(',')")
	# One derived tree serves both manifests when they name the same commits.
	if [ "$commit $carried" != "$made" ]; then
		drop_tree
		# The derived tree: a conflict under a root the copies read fails; a path left outside them is printed.
		"$root/automation/gen/vendor-chord-derive.sh" "$repo" "$work/tree" "$commit" "$carried" |
			sed "s|^left |vendor-chord-carried-check: outside the copied roots, left at the derived tree's side: Pi |"
		made="$commit $carried"
	fi
	mkdir "$work/$name"
	PI_TREE="$work/tree" PI_COMMIT="$commit" PI_CARRIED="$carried" VENDOR_CHORD_OUT="$work/$name" \
		"$root/automation/gen/vendor-chord-$name.sh" >/dev/null
	if cmp -s "$work/$name/manifest.json" "$manifest"; then
		echo "vendor-chord-carried-check: $dir is Pi $commit plus [${carried//,/ }]"
	else
		echo "vendor-chord-carried-check: $dir is NOT Pi $commit plus [${carried//,/ }]; the manifests differ:" >&2
		diff "$manifest" "$work/$name/manifest.json" | head -20 >&2 || true
		status=1
	fi
done
exit $status
