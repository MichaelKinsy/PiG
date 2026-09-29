import { projectForkCurrentStateWrite, selectBranchFork } from "../fork-policy.js";
import { fileValue, parseJsonlTransaction, publishJsonl, readJsonlHeader } from "./io.js";
import { JSONL_STORAGE_VERSION } from "./types.js";
function physicalKey(namespace, key) {
    return `${namespace}\u0000${key}`;
}
async function readJsonlForkHeader(reader, source, context) {
    const parsed = await readJsonlHeader(reader, source.path, context);
    if (parsed.format !== "v4") {
        throw new Error(`Invalid JSONL storage ${source.path}: expected format 4 header`);
    }
    const header = parsed.header;
    if (header.id !== source.id || header.cwd !== source.cwd) {
        throw new Error(`Session identity does not match header: ${source.id}`);
    }
    if (header.storageVersion !== JSONL_STORAGE_VERSION) {
        throw new Error(`Session ${source.id} uses unsupported storage version ${header.storageVersion}`);
    }
    return header;
}
function reachesForkBoundary(writes, stopBeforeSeq) {
    if (stopBeforeSeq === undefined || writes.length === 0)
        return false;
    const first = writes[0];
    const last = writes.at(-1);
    if (first.seq >= stopBeforeSeq)
        return true;
    if (last.seq >= stopBeforeSeq) {
        throw new Error(`JSONL transaction crosses fork sequence boundary ${stopBeforeSeq}`);
    }
    return false;
}
/** Read complete transactions after the header, never splitting a transaction at the sequence boundary. */
async function* readJsonlForkTransactions(reader, path, stopBeforeSeq, context) {
    while (true) {
        const line = fileValue(await reader.readLine(context), `Failed to read JSONL fork source ${path}`);
        if (line === undefined || !line.terminated)
            break;
        const writes = parseJsonlTransaction(line.text);
        if (reachesForkBoundary(writes, stopBeforeSeq))
            break;
        yield writes;
    }
}
class JsonlForkIndex {
    currentScalarSeqs = new Map();
    branchTips = new Map();
    firstSurvivingListSeqs = new Map();
    entryParents = new Map();
    copiedEntryIds = new Set();
    laneConfigs = new Set();
    laneStates = new Set();
    applyEntry(id, parentId) {
        this.entryParents.set(id, parentId);
    }
    applyWrites(writes) {
        for (const write of writes) {
            switch (write.kind) {
                case "entry":
                    this.applyEntry(write.id, write.parentId);
                    break;
                case "value": {
                    const key = physicalKey(write.namespace, write.key);
                    if (write.op === "delete") {
                        this.currentScalarSeqs.delete(key);
                    }
                    else {
                        this.currentScalarSeqs.set(key, write.seq);
                    }
                    this.applyLaneValue(write);
                    break;
                }
                case "list": {
                    const key = physicalKey(write.namespace, write.key);
                    if (write.op === "delete")
                        this.firstSurvivingListSeqs.delete(key);
                    else if (!this.firstSurvivingListSeqs.has(key))
                        this.firstSurvivingListSeqs.set(key, write.seq);
                    break;
                }
                case "usage":
                    break;
            }
        }
    }
    applyLaneValue(write) {
        const present = write.op === "set";
        switch (write.namespace) {
            case "pi.branch.tip":
                if (present)
                    this.branchTips.set(write.key, write.value);
                else
                    this.branchTips.delete(write.key);
                break;
            case "pi.lane.config":
                if (present)
                    this.laneConfigs.add(write.key);
                else
                    this.laneConfigs.delete(write.key);
                break;
            case "pi.lane.state":
                if (present)
                    this.laneStates.add(write.key);
                else
                    this.laneStates.delete(write.key);
                break;
        }
    }
    getBranchTip(branch) {
        return this.branchTips.get(branch);
    }
    hasCompleteLane(branch) {
        return this.laneConfigs.has(branch) && this.laneStates.has(branch);
    }
    getCurrentScalarSeq(namespace, key) {
        return this.currentScalarSeqs.get(physicalKey(namespace, key));
    }
    isSurvivingListElement(namespace, key, seq) {
        const firstSeq = this.firstSurvivingListSeqs.get(physicalKey(namespace, key));
        return firstSeq !== undefined && seq >= firstSeq;
    }
    getParent(entryId) {
        return this.entryParents.get(entryId);
    }
    selectEntry(entryId) {
        this.copiedEntryIds.add(entryId);
    }
    isEntrySelected(entryId) {
        return this.copiedEntryIds.has(entryId);
    }
}
/**
 * Validate the source lanes and return the fork plan.
 *
 * Branch scope updates JsonlForkIndex's selected entries via index.selectEntry(): it selects the
 * destination tip and its ancestors after validating entryId and applying "at"/"before". The plan
 * records the branch name and destination tip; the copy pass uses the index to filter entries and labels.
 *
 * Tree scope leaves the index's selected entries unchanged because the copy pass keeps every entry.
 * Both formats use the same indexed metadata; this function performs no file I/O.
 */
function selectJsonlFork(index, options) {
    if (options.scope === "tree")
        return { scope: "tree" };
    const plan = selectBranchFork(options, {
        tip: index.getBranchTip(options.branch),
        getParent: (entryId) => index.getParent(entryId),
        selectEntry: (entryId) => index.selectEntry(entryId),
    });
    if (!index.hasCompleteLane(options.branch)) {
        throw new Error(`Source branch ${JSON.stringify(options.branch)} is not a configured AgentLane`);
    }
    return plan;
}
function projectJsonlForkWrite(write, index, plan, isEntryCopied) {
    switch (write.kind) {
        case "entry":
            return isEntryCopied(write.id) ? write : undefined;
        case "value":
            if (write.op !== "set")
                return undefined;
            if (index.getCurrentScalarSeq(write.namespace, write.key) !== write.seq)
                return undefined;
            return projectForkCurrentStateWrite(write, plan, isEntryCopied);
        case "list":
            if (write.op !== "append")
                return undefined;
            if (!index.isSurvivingListElement(write.namespace, write.key, write.seq))
                return undefined;
            return projectForkCurrentStateWrite(write, plan, isEntryCopied);
        case "usage":
            return undefined;
    }
}
/**
 * Build the index used to select branch ancestry and identify current scalar/list writes.
 * Retains entry parent links, current-row sequences, and lane inventory, not entry payloads.
 * Returns the index and the source's nextSeq high-water mark; no destination writes occur here.
 *
 * V3: LegacyV3Source already scanned the file and assigned normalized IDs/sequences. Copy its
 * in-memory entry structures and derived values without reopening the file or calling writes(),
 * which would unnecessarily parse payloads and reconstruct compaction tails before fork selection.
 *
 * V4: Open the source file and fold complete transactions into the index. For an open source,
 * stop at the captured commit-queue sequence boundary. For a closed source, scan to EOF or a torn
 * final line and derive nextSeq from the header and highest complete write sequence.
 * The later copy pass reopens the source file in both formats and emits only selected writes.
 */
async function indexForkInput(input, fileSystem, context) {
    const index = new JsonlForkIndex();
    if (input.kind === "legacy-v3") {
        for (const entry of input.normalized.entryStructures())
            index.applyEntry(entry.id, entry.parentId);
        index.applyWrites(input.normalized.values);
        return { index, nextSeq: input.normalized.nextSeq };
    }
    const reader = fileValue(await fileSystem.openTextLineReader(input.metadata.path, context), `Failed to open JSONL fork source ${input.metadata.path}`);
    try {
        const header = await readJsonlForkHeader(reader, input.metadata, context);
        const stopBeforeSeq = input.kind === "open" ? input.nextSeq : undefined;
        let highestCompleteSeq = 0;
        for await (const writes of readJsonlForkTransactions(reader, input.metadata.path, stopBeforeSeq, context)) {
            index.applyWrites(writes);
            if (writes.length !== 0)
                highestCompleteSeq = writes.at(-1).seq;
        }
        return {
            index,
            nextSeq: input.kind === "open" ? input.nextSeq : Math.max(header.nextSeq ?? 1, highestCompleteSeq + 1),
        };
    }
    finally {
        await reader.close(context);
    }
}
/** Yield source writes; the caller owns final projection and filtering. */
async function* streamForkWrites(input, fileSystem, stopBeforeSeq, isEntryCopied, context) {
    if (input.kind === "legacy-v3") {
        // V3 filters early to avoid rereading messages and reconstructing unselected compaction tails.
        // V4 already stores complete payloads, so it streams all captured writes below.
        // Both formats still pass through projectJsonlForkWrite() for final filtering and transformation.
        yield* input.normalized.writes(context, isEntryCopied);
        return;
    }
    const reader = fileValue(await fileSystem.openTextLineReader(input.metadata.path, context), `Failed to open JSONL fork source ${input.metadata.path}`);
    try {
        await readJsonlForkHeader(reader, input.metadata, context);
        for await (const writes of readJsonlForkTransactions(reader, input.metadata.path, stopBeforeSeq, context)) {
            yield* writes;
        }
    }
    finally {
        await reader.close(context);
    }
}
/**
 * Index the source, validate the requested fork, and stream selected entries and current state
 * into an atomically published format-4 destination without modifying the source.
 * Preserve copied sequences and the source's nextSeq while excluding usage and open-operation state.
 * Source files must not be replaced or edited between passes; later append-only writes are excluded
 * by the captured sequence boundary or legacy record count. Does not open the destination Session.
 */
export async function runJsonlFork(options, context) {
    const { index, nextSeq } = await indexForkInput(options.input, options.fileSystem, context);
    let fork = options.fork;
    if (options.input.kind === "legacy-v3" && fork.scope === "branch" && fork.entryId !== undefined) {
        fork = { ...fork, entryId: options.input.normalized.translateForkEntryId(fork.entryId) };
    }
    const plan = selectJsonlFork(index, fork);
    const isEntryCopied = (entryId) => {
        if (plan.scope === "tree")
            return true;
        return index.isEntrySelected(entryId);
    };
    await publishJsonl(options.fileSystem, options.destinationPath, { ...options.destinationHeader, nextSeq }, context, async (append) => {
        const sourceWrites = streamForkWrites(options.input, options.fileSystem, nextSeq, isEntryCopied, context);
        for await (const write of sourceWrites) {
            const projected = projectJsonlForkWrite(write, index, plan, isEntryCopied);
            if (projected !== undefined)
                await append([projected]);
        }
    });
}
//# sourceMappingURL=fork.js.map