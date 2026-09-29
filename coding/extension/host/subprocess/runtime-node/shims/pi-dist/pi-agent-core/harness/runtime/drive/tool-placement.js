import { insertEntry, insertUsage } from "../../session/commit.js";
import { SessionInvariantError } from "../../session/session.js";
import { operationScopeOf, } from "../../session/types.js";
import { branchTip, deleteValue, operationToolArgsPrefix, pendingEntry, setValue } from "../../session/values.js";
export async function readToolBatchSource(lane, drive, batch) {
    return lane.command(async (_state, reader) => {
        const entry = (await reader.getEntries([batch.assistantEntryId], drive.context)).get(batch.assistantEntryId);
        if (entry?.type !== "message" || entry.message.role !== "assistant") {
            throw new SessionInvariantError("Tool batch assistant entry is invalid");
        }
        const calls = new Map();
        for (const call of batch.calls) {
            const block = entry.message.content[call.sourceIndex];
            if (block?.type !== "toolCall") {
                throw new SessionInvariantError(`Tool call source index ${call.sourceIndex} does not name a tool-call block`);
            }
            calls.set(call.sourceIndex, block);
        }
        return { kind: "return", result: { assistant: entry.message, calls } };
    }, drive.context);
}
function isToolResultMessage(value) {
    return typeof value === "object" && value !== null && "role" in value && value.role === "toolResult";
}
export function toolCallFor(sources, call) {
    const source = sources.calls.get(call.sourceIndex);
    if (source === undefined) {
        throw new SessionInvariantError(`Tool call source index ${call.sourceIndex} is invalid`);
    }
    return source;
}
export function withToolBatch(run, batch) {
    return { ...operationScopeOf(run), at: "tools", batch };
}
async function readPlacement(lane, drive, sources) {
    return lane.command(async (state, reader) => {
        const operation = state.operation;
        if (operation?.state.at !== "tools")
            return { kind: "return", result: undefined };
        const current = operation.state.batch;
        let first = current.calls.findIndex((call) => call.status !== "completed");
        if (first === -1)
            return { kind: "return", result: undefined };
        const ready = [];
        while (first < current.calls.length) {
            const call = current.calls[first];
            if (call.status !== "outcome_ready")
                break;
            ready.push(call);
            first += 1;
        }
        if (ready.length === 0)
            return { kind: "return", result: undefined };
        const items = [];
        for (const call of ready) {
            const stored = await reader.getValue(pendingEntry(call.resultEntryId), drive.context);
            if (stored?.value.type !== "message" || !isToolResultMessage(stored.value.payload)) {
                throw new SessionInvariantError(`Tool call ${call.resultEntryId} is missing its staged result`);
            }
            const source = toolCallFor(sources, call);
            if (stored.value.payload.toolCallId !== source.id || stored.value.payload.toolName !== source.name) {
                throw new SessionInvariantError(`Tool call ${call.resultEntryId} has a mismatched staged result`);
            }
            items.push({ call, message: stored.value.payload });
        }
        let turnResults;
        if (first === current.calls.length) {
            const placedIds = current.calls
                .filter((call) => call.status === "completed")
                .map((call) => call.resultEntryId);
            const placed = await reader.getEntries(placedIds, drive.context);
            const staged = new Map(items.map((item) => [item.call.resultEntryId, item.message]));
            turnResults = current.calls.map((call) => {
                const message = staged.get(call.resultEntryId) ??
                    (() => {
                        const entry = placed.get(call.resultEntryId);
                        return entry?.type === "message" && isToolResultMessage(entry.message) ? entry.message : undefined;
                    })();
                if (message === undefined) {
                    throw new SessionInvariantError(`Completed tool call ${call.resultEntryId} is missing its result entry`);
                }
                return message;
            });
        }
        return {
            kind: "return",
            result: { items, ...(turnResults === undefined ? {} : { turnResults }) },
        };
    }, drive.context);
}
async function commitPlacement(lane, drive, capability, read) {
    const usageIds = read.items.map((item) => item.message.usage === undefined ? undefined : lane.session.idGenerator.next());
    return lane.settleOperation(capability, async (state, run, _meta, reader) => {
        const current = run.batch;
        const writes = [];
        const eventEntries = [];
        const eventUsage = [];
        let parentId = state.tipId;
        for (const [index, item] of read.items.entries()) {
            const entry = {
                id: item.call.resultEntryId,
                parentId,
                type: "message",
                message: item.message,
                ...(item.call.terminate ? { terminate: true } : {}),
            };
            eventEntries.push({ entry, seqIndex: writes.length });
            writes.push(insertEntry(entry), deleteValue(pendingEntry(item.call.resultEntryId)));
            const usageId = usageIds[index];
            if (usageId !== undefined && item.message.usage !== undefined) {
                const row = {
                    id: usageId,
                    usage: item.message.usage,
                    entryId: item.call.resultEntryId,
                    adjustment: false,
                };
                eventUsage.push({ row, seqIndex: writes.length });
                writes.push(insertUsage(row));
            }
            parentId = item.call.resultEntryId;
        }
        const completedCalls = current.calls.map((call) => {
            const item = read.items.find((candidate) => candidate.call.sourceIndex === call.sourceIndex &&
                candidate.call.resultEntryId === call.resultEntryId);
            return item === undefined
                ? call
                : {
                    status: "completed",
                    sourceIndex: call.sourceIndex,
                    resultEntryId: call.resultEntryId,
                    terminate: item.call.terminate,
                };
        });
        const complete = completedCalls.every((call) => call.status === "completed");
        writes.push(setValue(branchTip(lane.name), parentId));
        let nextRun;
        if (complete) {
            const allTerminate = completedCalls.every((call) => call.status === "completed" && call.terminate);
            const checkpoint = {
                ...operationScopeOf(run),
                at: "checkpoint",
                continuation: allTerminate
                    ? { kind: "may_finish", includeFinalAssistant: false }
                    : { kind: "need_assistant", overflowRecoveryUsed: false },
                triggerEntryId: parentId,
            };
            nextRun = checkpoint;
            const args = await reader.scanValues(operationToolArgsPrefix(drive.operationId, capability.batch.turnId), drive.context);
            writes.push(...args.map(({ address }) => deleteValue(address)));
        }
        else {
            nextRun = withToolBatch(run, { ...current, calls: completedCalls });
        }
        return {
            kind: "commit",
            writes,
            operationState: nextRun,
            lane: { tipId: parentId, configuration: state.configuration },
            materialize: () => complete,
            events: (commit) => {
                const events = [];
                for (const { entry, seqIndex } of eventEntries) {
                    events.push({
                        type: "entry_added",
                        lane: lane.name,
                        entry: { ...entry, seq: commit.seqs[seqIndex], timestamp: commit.timestamp },
                    });
                    const usage = eventUsage.find((candidate) => candidate.row.entryId === entry.id);
                    if (usage !== undefined) {
                        events.push({
                            type: "usage",
                            lane: lane.name,
                            row: { ...usage.row, seq: commit.seqs[usage.seqIndex] },
                            totals: commit.stats.usage,
                        });
                    }
                }
                return events;
            },
        };
    }, drive.context);
}
export async function materializeReady(lane, drive, capability, sources, recovery) {
    const read = await readPlacement(lane, drive, sources);
    if (read === undefined)
        return;
    await lane.emitBatch(read.items.flatMap(({ call, message }) => [
        {
            type: "message_start",
            lane: lane.name,
            runId: drive.operationId,
            message,
            ...(recovery ? { recovery: true } : {}),
        },
        {
            type: "message_end",
            lane: lane.name,
            runId: drive.operationId,
            message,
            entryId: call.resultEntryId,
            ...(recovery ? { recovery: true } : {}),
        },
    ]), drive.context);
    const complete = await commitPlacement(lane, drive, capability, read);
    if (complete && read.turnResults !== undefined) {
        await lane.emitBatch([
            {
                type: "turn_end",
                lane: lane.name,
                runId: drive.operationId,
                turnId: capability.batch.turnId,
                message: sources.assistant,
                toolResults: read.turnResults,
                ...(recovery ? { recovery: true } : {}),
            },
        ], drive.context);
    }
}
//# sourceMappingURL=tool-placement.js.map