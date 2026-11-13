// The provenance a vendored chord tree's manifest records, shared by vendor-chord-plugins-node.mjs and
// vendor-chord-surfaces-client.mjs: the Pi commit copied, the pin the copy was made against, that commit as `catchup` when it
// is not the pin's (the commit the manifest may differ from the pin by: the Go tests accept exactly it, while the pin stays
// put), and the Pi commits cherry-picked onto it to make the tree as `carried` (PI_CARRIED of
// automation/gen/vendor-chord-common.sh: comma-separated full ids, in Pi's order).
export function provenance(commit, pin, carriedList = "") {
	const carried = carriedList === "" ? [] : carriedList.split(",");
	for (const id of carried) {
		if (!/^[0-9a-f]{40}$/.test(id)) throw new Error(`PI_CARRIED: ${JSON.stringify(id)} is not a full Pi commit id`);
	}
	return {
		commit,
		pin,
		...(commit === pin ? {} : { catchup: commit }),
		...(carried.length === 0 ? {} : { carried }),
	};
}
