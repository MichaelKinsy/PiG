import { projectForkCurrentStateWrite, selectBranchFork } from "./fork-policy.js";
import { branchTip, laneConfig, laneState, value } from "./values.js";
function storedValuesInNamespace(values, address) {
    return values.filter((stored) => stored.address.namespace === address.namespace);
}
function findStoredValue(values, address) {
    return values.find((stored) => stored.address.namespace === address.namespace && stored.address.key === address.key);
}
/** Build the complete logical state for a forked destination session. */
export function createForkSnapshot(source, options) {
    const sourceEntries = new Map(source.entries.map((entry) => [entry.id, entry]));
    const sourceTips = storedValuesInNamespace(source.scalarValues, branchTip(""));
    validateForkSourceSnapshot(source, sourceEntries, sourceTips, options);
    const { entryIds, plan } = selectForkContents(sourceEntries, sourceTips, options);
    const entries = new Map();
    for (const id of entryIds)
        entries.set(id, sourceEntries.get(id));
    const scalarValues = [];
    let nextSeq = Math.max(0, ...[...entries.values()].map((entry) => entry.seq)) + 1;
    for (const stored of source.scalarValues) {
        const projected = projectForkCurrentStateWrite({
            kind: "value",
            op: "set",
            seq: stored.seq,
            namespace: stored.address.namespace,
            key: stored.address.key,
            value: stored.value,
        }, plan, (entryId) => entryIds.has(entryId));
        if (projected !== undefined) {
            scalarValues.push({
                address: value(projected.namespace, projected.key),
                value: projected.value,
                seq: nextSeq++,
            });
        }
    }
    return { entries, scalarValues, nextSeq };
}
function selectForkContents(sourceEntries, sourceTips, options) {
    const entryIds = new Set();
    if (options.scope === "tree") {
        for (const id of sourceEntries.keys())
            entryIds.add(id);
        return { entryIds, plan: { scope: "tree" } };
    }
    const sourceTip = sourceTips.find((stored) => stored.address.key === options.branch);
    const plan = selectBranchFork(options, {
        tip: sourceTip?.value,
        getParent: (entryId) => sourceEntries.get(entryId)?.parentId,
        selectEntry: (entryId) => entryIds.add(entryId),
    });
    return { entryIds, plan };
}
function validateForkSourceSnapshot(source, sourceEntries, sourceTips, options) {
    const sourceTipKeys = new Set(sourceTips.map((stored) => stored.address.key));
    for (const stored of source.scalarValues) {
        if ((stored.address.namespace === laneConfig("").namespace ||
            stored.address.namespace === laneState("").namespace) &&
            !sourceTipKeys.has(stored.address.key)) {
            throw new Error(`Source session branch ${JSON.stringify(stored.address.key)} is missing branch.tip`);
        }
    }
    for (const tip of sourceTips) {
        const configuration = findStoredValue(source.scalarValues, laneConfig(tip.address.key));
        const state = findStoredValue(source.scalarValues, laneState(tip.address.key));
        if ((configuration === undefined) !== (state === undefined)) {
            throw new Error(`Source session branch ${JSON.stringify(tip.address.key)} has incomplete lane state`);
        }
        if (options.scope === "branch" && tip.address.key === options.branch && configuration === undefined) {
            throw new Error(`Source branch ${JSON.stringify(options.branch)} is not a configured AgentLane`);
        }
        if ((source.entriesComplete !== false || options.scope === "tree") &&
            tip.value !== null &&
            !sourceEntries.has(tip.value)) {
            throw new Error(`Source session branch ${JSON.stringify(tip.address.key)} has an unknown tip`);
        }
    }
}
//# sourceMappingURL=fork.js.map