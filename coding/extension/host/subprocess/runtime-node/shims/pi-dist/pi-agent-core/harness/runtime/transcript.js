import { materializeCommittedEntry } from "../session/commit.js";
import { buildSessionContext } from "../session/context.js";
import { SessionInvariantError } from "../session/session.js";
import { pendingEntry } from "../session/values.js";
export function chainEntries(parentId, items) {
    return items.map((item) => {
        const entry = { ...item, parentId };
        parentId = item.id;
        return entry;
    });
}
export function entryLifecycleEvents(entry, lane, runId) {
    const operation = runId === undefined ? {} : { runId };
    return entry.type === "message"
        ? [
            { type: "message_start", lane, ...operation, message: entry.message },
            { type: "message_end", lane, ...operation, message: entry.message, entryId: entry.id },
            { type: "entry_added", lane, entry },
        ]
        : [{ type: "entry_added", lane, entry }];
}
export function committedEntryEvents(entries, commit, lane, runId, firstWriteIndex = 0) {
    return entries.flatMap((entry, index) => entryLifecycleEvents(materializeCommittedEntry(entry, commit.seqs[firstWriteIndex + index], commit.timestamp), lane, runId));
}
export function readBoundedEntries(lane, drive, capability) {
    return lane.continueOperation(capability, async (state, _current, _meta, reader) => {
        if (state.tipId === null)
            throw new SessionInvariantError("Run operation has no Branch tip");
        const entries = await reader.scanBranch({ start: state.tipId, stopAtType: "compaction", order: "newestFirst" }, drive.context);
        return { kind: "return", result: entries.reverse() };
    }, drive.context);
}
export async function readBoundedContext(lane, drive, capability) {
    const entries = await readBoundedEntries(lane, drive, capability);
    if (entries.kind === "cancel_requested")
        return entries;
    return {
        kind: "result",
        value: await buildSessionContext(entries.value, { entryProjectors: lane.readConfig().entryProjectors }, drive.context),
    };
}
export function readLaneQueues(reader, inbox, context) {
    return Promise.all(inbox.map(async (item) => {
        const stored = await reader.getValue(pendingEntry(item.entryId), context);
        if (stored === undefined) {
            throw new SessionInvariantError(`Pending ${item.kind} entry ${item.entryId} is missing its payload`);
        }
        if (stored.value.type === "message") {
            return { entryId: item.entryId, kind: item.kind, type: "message", message: stored.value.payload };
        }
        if (item.kind !== "write") {
            throw new SessionInvariantError(`Pending ${item.kind} entry ${item.entryId} is not a message`);
        }
        return {
            entryId: item.entryId,
            kind: "write",
            type: "custom",
            customType: stored.value.customType,
            ...(stored.value.payload === undefined ? {} : { data: stored.value.payload }),
        };
    }));
}
export function readPendingMessages(reader, ids, description, context) {
    return Promise.all(ids.map(async (entryId) => {
        const value = await reader.getValue(pendingEntry(entryId), context);
        if (value?.value.type !== "message") {
            throw new SessionInvariantError(`${description} ${entryId} is missing its message payload`);
        }
        return { entryId, message: value.value.payload };
    }));
}
//# sourceMappingURL=transcript.js.map