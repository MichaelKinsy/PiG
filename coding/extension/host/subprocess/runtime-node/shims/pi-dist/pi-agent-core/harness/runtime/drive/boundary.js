import { DEFAULT_MAX_AGENT_RETRY_DELAY_MS } from "../../../../pi-ai/sdk-bundle/index.js";
import { insertEntry } from "../../session/commit.js";
import { SessionInvariantError } from "../../session/session.js";
import { branchTip, deleteValue, pendingEntry, setValue } from "../../session/values.js";
import { committedEntryEvents, entryLifecycleEvents, readBoundedContext, readLaneQueues } from "../transcript.js";
import { operationCleanupWrites, operationResultRecord } from "./terminal.js";
export function normalizedRetryPolicy(lane) {
    const retry = lane.readConfig().retryPolicy;
    return {
        maxAttempts: retry.enabled ? retry.maxRetries + 1 : 1,
        baseDelayMs: retry.baseDelayMs,
        maxAgentDelayMs: retry.maxAgentDelayMs ?? DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
    };
}
export function assistantReadyAtBoundary(lane, state, scope, triggerEntryId, overflowRecoveryUsed) {
    const config = lane.readConfig();
    return {
        ...scope,
        at: "assistant.ready",
        generationContext: {
            stepId: lane.session.idGenerator.next(),
            triggerEntryId,
            configuration: state.configuration,
            streamOptions: config.streamOptions,
            retryPolicy: normalizedRetryPolicy(lane),
            overflowRecoveryUsed,
        },
        nextAttempt: 1,
    };
}
/** Select and materialize one boundary's lane-owned input without committing it. */
export async function planBoundaryInbox(lane, drive, state, scope, reader, tipId, followUpWhenNoTrigger) {
    const steer = state.inbox.filter((item) => item.kind === "steer");
    const selectedSteer = scope.settings.steeringMode === "all" ? steer : steer.slice(0, 1);
    let selected = state.inbox.filter((item) => item.kind === "write" || selectedSteer.some((candidate) => candidate.entryId === item.entryId));
    const load = (items) => Promise.all(items.map(async (item) => {
        const stored = await reader.getValue(pendingEntry(item.entryId), drive.context);
        if (stored === undefined) {
            throw new SessionInvariantError(`Pending ${item.kind} entry ${item.entryId} is missing its payload`);
        }
        if (item.kind !== "write" && stored.value.type !== "message") {
            throw new SessionInvariantError(`Queued ${item.kind} entry ${item.entryId} is not a message`);
        }
        return { item, pending: stored.value };
    }));
    let pending = await load(selected);
    const projects = (value) => value.type === "message" || lane.readConfig().entryProjectors[value.customType] !== undefined;
    if (followUpWhenNoTrigger && !pending.some(({ pending: value }) => projects(value))) {
        const followUp = state.inbox.filter((item) => item.kind === "followUp");
        const selectedFollowUp = scope.settings.followUpMode === "all" ? followUp : followUp.slice(0, 1);
        selected = [...selected, ...selectedFollowUp].sort((a, b) => state.inbox.indexOf(a) - state.inbox.indexOf(b));
        pending = await load(selected);
    }
    let parentId = tipId;
    let triggerEntryId;
    const entries = pending.map(({ item, pending: value }) => {
        const entry = value.type === "message"
            ? { id: item.entryId, parentId, type: "message", message: value.payload }
            : {
                id: item.entryId,
                parentId,
                type: "custom",
                customType: value.customType,
                ...(value.payload === undefined ? {} : { data: value.payload }),
            };
        parentId = item.entryId;
        if (projects(value))
            triggerEntryId = item.entryId;
        return entry;
    });
    const selectedIds = new Set(selected.map((item) => item.entryId));
    const inbox = state.inbox.filter((item) => !selectedIds.has(item.entryId));
    const queues = selected.length === 0 ? undefined : await readLaneQueues(reader, inbox, drive.context);
    return {
        entries,
        writes: [
            ...entries.map((entry) => insertEntry(entry)),
            ...selected.map((item) => deleteValue(pendingEntry(item.entryId))),
            ...(entries.length === 0 ? [] : [setValue(branchTip(lane.name), parentId)]),
        ],
        tipId: parentId,
        inbox,
        ...(triggerEntryId === undefined ? {} : { triggerEntryId }),
        ...(queues === undefined ? {} : { queues }),
    };
}
export function boundaryPlacementEvents(placement, commit, firstWriteIndex, lane, runId) {
    return [
        ...committedEntryEvents(placement.entries, commit, lane, runId, firstWriteIndex),
        ...(placement.queues === undefined ? [] : [{ type: "queue_update", lane, queues: placement.queues }]),
    ];
}
/** Replan after before_run_end and commit either renewed work or the terminal run result. */
export async function finishRunBoundary(lane, drive, capability, continuation, plannedEntryIds, pendingEvents = []) {
    const context = await readBoundedContext(lane, drive, capability);
    if (context.kind === "cancel_requested")
        return { kind: "continue" };
    const hook = await lane.hooks.runWithGate("before_run_end", { lane: lane.name, runId: drive.operationId, messages: context.value }, drive.gate, drive.context);
    const followUp = hook?.followUp === undefined
        ? undefined
        : {
            id: lane.session.idGenerator.next(),
            message: { role: "user", content: hook.followUp, timestamp: Date.now() },
        };
    const result = await lane.continueOperation(capability, async (state, current, meta, reader) => {
        const placement = await planBoundaryInbox(lane, drive, state, current, reader, state.tipId, true);
        if (placement.triggerEntryId !== undefined) {
            return {
                kind: "commit",
                writes: placement.writes,
                operationState: assistantReadyAtBoundary(lane, state, current, placement.triggerEntryId, false),
                lane: { tipId: placement.tipId, inbox: placement.inbox },
                materialize: () => ({ kind: "continue" }),
                events: (commit) => [
                    ...pendingEvents,
                    ...boundaryPlacementEvents(placement, commit, 0, lane.name, drive.operationId),
                ],
            };
        }
        const hookPlanIsCurrent = placement.entries.length === plannedEntryIds.length &&
            placement.entries.every((entry, index) => entry.id === plannedEntryIds[index]);
        if (hookPlanIsCurrent && followUp !== undefined) {
            const entry = {
                id: followUp.id,
                parentId: placement.tipId,
                type: "message",
                message: followUp.message,
            };
            const entryWriteIndex = placement.writes.length;
            return {
                kind: "commit",
                writes: [...placement.writes, insertEntry(entry), setValue(branchTip(lane.name), followUp.id)],
                operationState: assistantReadyAtBoundary(lane, state, current, followUp.id, false),
                lane: { tipId: followUp.id, inbox: placement.inbox },
                materialize: () => ({ kind: "continue" }),
                events: (commit) => [
                    ...pendingEvents,
                    ...boundaryPlacementEvents(placement, commit, 0, lane.name, drive.operationId),
                    ...entryLifecycleEvents({ ...entry, seq: commit.seqs[entryWriteIndex], timestamp: commit.timestamp }, lane.name, drive.operationId),
                ],
            };
        }
        if (placement.tipId === null)
            throw new SessionInvariantError("Completed run has no tip");
        if (continuation.includeFinalAssistant && current.latestAssistantEntryId === null) {
            throw new SessionInvariantError("Completed run is missing its final assistant");
        }
        const record = operationResultRecord(meta, "completed", placement.tipId);
        const cleanup = await operationCleanupWrites(reader, drive.operationId, current, drive.context);
        return {
            kind: "finish",
            writes: [...placement.writes, ...cleanup],
            record,
            lane: { tipId: placement.tipId, inbox: placement.inbox },
            materialize: () => ({ kind: "settled", outcome: record }),
            events: (commit) => [
                ...pendingEvents,
                ...boundaryPlacementEvents(placement, commit, 0, lane.name, drive.operationId),
                {
                    type: "run_end",
                    lane: lane.name,
                    runId: drive.operationId,
                    status: "completed",
                    fromTipId: meta.sourceTipId,
                    tipId: placement.tipId,
                    endedAt: record.endedAt,
                },
            ],
        };
    }, drive.context);
    return result.kind === "cancel_requested" ? { kind: "continue" } : result.value;
}
//# sourceMappingURL=boundary.js.map