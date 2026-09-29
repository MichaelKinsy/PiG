import { addUsage, emptyUsage } from "../utils/usage.js";
import { prepareStorageCommit, validateCommittedWrites, } from "./commit.js";
import { projectForkCurrentStateWrite, selectBranchFork } from "./fork-policy.js";
import { branchTip, laneConfig, laneState, list, resolveListReadOptions, value, } from "./values.js";
function physicalKey(namespace, key) {
    return `${namespace}\u0000${key}`;
}
function compareKeys(left, right) {
    const leftCodePoints = Array.from(left, (character) => character.codePointAt(0));
    const rightCodePoints = Array.from(right, (character) => character.codePointAt(0));
    const length = Math.min(leftCodePoints.length, rightCodePoints.length);
    for (let index = 0; index < length; index++) {
        const difference = leftCodePoints[index] - rightCodePoints[index];
        if (difference !== 0)
            return difference;
    }
    return leftCodePoints.length - rightCodePoints.length;
}
/**
 * Complete materialized session state for MemoryStorage and JsonlStorage.
 *
 * This is intentionally unsuitable for database backends and long-running sessions that may not fit in memory.
 * Those backends should query indexed durable state and update durable aggregates within each commit transaction.
 */
export class InMemoryStorageState {
    entries;
    entriesBySeq;
    scalarValues;
    listValues;
    usage;
    stats;
    nextSeq;
    constructor() {
        this.entries = new Map();
        this.entriesBySeq = [];
        this.scalarValues = new Map();
        this.listValues = new Map();
        this.usage = new Map();
        this.stats = { messageCount: 0, usage: emptyUsage() };
        this.nextSeq = 1;
    }
    prepareCommit(writes, timestamp) {
        const prepared = prepareStorageCommit(writes, this.nextSeq, timestamp);
        this.validateCommitted(prepared.writes);
        return prepared;
    }
    validateCommitted(writes) {
        validateCommittedWrites(writes, this.nextSeq, {
            hasEntryOrUsageId: (id) => this.entries.has(id) || this.usage.has(id),
            hasEntryId: (id) => this.entries.has(id),
        });
    }
    /** Apply writes already accepted by validateCommitted() and return the post-apply totals. */
    applyValidated(writes) {
        for (const write of writes) {
            switch (write.kind) {
                case "entry": {
                    const { kind: _kind, ...entry } = write;
                    this.entries.set(entry.id, entry);
                    this.entriesBySeq.push(entry);
                    if (entry.type === "message")
                        this.stats = { ...this.stats, messageCount: this.stats.messageCount + 1 };
                    break;
                }
                case "usage": {
                    const { kind: _kind, ...row } = write;
                    this.usage.set(row.id, row);
                    this.stats = { ...this.stats, usage: addUsage(this.stats.usage, row.usage) };
                    break;
                }
                case "value":
                    if (write.op === "delete")
                        this.scalarValues.delete(physicalKey(write.namespace, write.key));
                    else
                        this.applyValueSetOrListAppend(write);
                    break;
                case "list":
                    if (write.op === "delete")
                        this.listValues.delete(physicalKey(write.namespace, write.key));
                    else
                        this.applyValueSetOrListAppend(write);
                    break;
            }
            this.nextSeq = write.seq + 1;
        }
        return this.stats;
    }
    createFork(options) {
        const plan = this.selectForkPlan(options);
        const isEntryCopied = (entryId) => {
            if (plan.scope === "tree")
                return true;
            return plan.entryIds.has(entryId);
        };
        const destination = new InMemoryStorageState();
        let messageCount = 0;
        for (const entry of this.entriesBySeq) {
            if (!isEntryCopied(entry.id))
                continue;
            destination.entries.set(entry.id, entry);
            destination.entriesBySeq.push(entry);
            if (entry.type === "message")
                messageCount++;
        }
        destination.stats = { ...destination.stats, messageCount };
        for (const stored of this.scalarValues.values()) {
            const projected = projectForkCurrentStateWrite({
                kind: "value",
                op: "set",
                seq: stored.seq,
                namespace: stored.address.namespace,
                key: stored.address.key,
                value: stored.value,
            }, plan, isEntryCopied);
            if (projected !== undefined)
                destination.applyValueSetOrListAppend(projected);
        }
        for (const stored of this.listValues.values()) {
            for (const element of stored.elements) {
                const projected = projectForkCurrentStateWrite({
                    kind: "list",
                    op: "append",
                    seq: element.seq,
                    namespace: stored.address.namespace,
                    key: stored.address.key,
                    value: element.value,
                }, plan, isEntryCopied);
                if (projected !== undefined)
                    destination.applyValueSetOrListAppend(projected);
            }
        }
        destination.nextSeq = this.nextSeq;
        return destination;
    }
    selectForkPlan(options) {
        if (options.scope === "tree")
            return { scope: "tree" };
        const entryIds = new Set();
        const plan = selectBranchFork(options, {
            tip: this.getValue(branchTip(options.branch))?.value,
            getParent: (entryId) => this.entries.get(entryId)?.parentId,
            selectEntry: (entryId) => entryIds.add(entryId),
        });
        if (this.getValue(laneConfig(options.branch)) === undefined ||
            this.getValue(laneState(options.branch)) === undefined) {
            throw new Error(`Source branch ${JSON.stringify(options.branch)} is not a configured AgentLane`);
        }
        return { ...plan, entryIds };
    }
    applyValueSetOrListAppend(write) {
        const key = physicalKey(write.namespace, write.key);
        if (write.kind === "value") {
            this.scalarValues.set(key, {
                address: value(write.namespace, write.key),
                value: write.value,
                seq: write.seq,
            });
            return;
        }
        const element = { seq: write.seq, value: write.value };
        const stored = this.listValues.get(key);
        if (stored === undefined) {
            this.listValues.set(key, {
                address: list(write.namespace, write.key),
                elements: [element],
            });
        }
        else {
            stored.elements.push(element);
        }
    }
    advanceNextSeq(nextSeq) {
        if (!Number.isSafeInteger(nextSeq) || nextSeq < 1) {
            throw new Error(`Invalid storage sequence high-water mark: ${nextSeq}`);
        }
        this.nextSeq = Math.max(this.nextSeq, nextSeq);
    }
    getEntries(ids) {
        const found = new Map();
        for (const id of ids) {
            const entry = this.entries.get(id);
            if (entry !== undefined)
                found.set(id, entry);
        }
        return found;
    }
    getValue(address) {
        return this.scalarValues.get(physicalKey(address.namespace, address.key));
    }
    scanValues(prefix) {
        return [...this.scalarValues.values()]
            .filter((stored) => stored.address.namespace === prefix.namespace && stored.address.key.startsWith(prefix.key))
            .sort((left, right) => compareKeys(left.address.key, right.address.key));
    }
    readList(address, options) {
        const resolved = resolveListReadOptions(options);
        const elements = this.listValues.get(physicalKey(address.namespace, address.key))?.elements ?? [];
        const filtered = elements.filter((element) => {
            if (resolved.cursor === undefined)
                return true;
            return resolved.order === "asc" ? element.seq > resolved.cursor.seq : element.seq < resolved.cursor.seq;
        });
        const ordered = resolved.order === "asc" ? filtered : [...filtered].reverse();
        return ordered.slice(0, resolved.limit);
    }
    scanBranch(query) {
        const start = this.entries.get(query.start);
        if (start === undefined)
            throw new Error(`Unknown branch start: ${query.start}`);
        const path = [];
        let entry = start;
        while (entry !== undefined) {
            path.push(entry);
            if (entry.parentId === null)
                break;
            entry = this.entries.get(entry.parentId);
            if (entry === undefined)
                throw new Error("Corrupt branch: missing parent");
        }
        if (query.order === "oldestFirst")
            path.reverse();
        const stopped = [];
        for (const candidate of path) {
            stopped.push(candidate);
            if (candidate.id === query.stopAtId || candidate.type === query.stopAtType)
                break;
        }
        const filtered = stopped
            .filter((candidate) => query.type === undefined || candidate.type === query.type)
            .filter((candidate) => query.customType === undefined || candidate.customType === query.customType)
            .filter((candidate) => query.cursor === undefined ||
            (query.order === "oldestFirst" ? candidate.seq > query.cursor.seq : candidate.seq < query.cursor.seq));
        return query.limit === undefined ? filtered : filtered.slice(0, Math.max(0, query.limit));
    }
    scanBranchStructure(query) {
        return this.scanBranch(query).map((entry) => ({
            id: entry.id,
            parentId: entry.parentId,
            seq: entry.seq,
            timestamp: entry.timestamp,
            type: entry.type,
            ...(entry.customType === undefined ? {} : { customType: entry.customType }),
        }));
    }
    scanEntries(query) {
        const limit = query.limit === undefined ? Number.POSITIVE_INFINITY : Math.max(0, Math.trunc(query.limit));
        const entries = [];
        const descending = query.order === "desc";
        let index = descending ? this.entriesBySeq.length - 1 : 0;
        while (index >= 0 && index < this.entriesBySeq.length && entries.length < limit) {
            const entry = this.entriesBySeq[index];
            if ((query.type === undefined || entry.type === query.type) &&
                (query.customType === undefined || entry.customType === query.customType) &&
                (query.fromSeq === undefined || entry.seq >= query.fromSeq) &&
                (query.toSeq === undefined || entry.seq <= query.toSeq)) {
                entries.push(entry);
            }
            index += descending ? -1 : 1;
        }
        return entries;
    }
    scanUsage(query) {
        const rows = [...this.usage.values()]
            .filter((row) => query.fromSeq === undefined || row.seq >= query.fromSeq)
            .filter((row) => query.toSeq === undefined || row.seq <= query.toSeq)
            .sort((left, right) => (query.order === "desc" ? right.seq - left.seq : left.seq - right.seq));
        return query.limit === undefined ? rows : rows.slice(0, Math.max(0, query.limit));
    }
    getStats() {
        return this.stats;
    }
    getNextSeq() {
        return this.nextSeq;
    }
}
//# sourceMappingURL=in-memory-storage-state.js.map