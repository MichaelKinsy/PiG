import { insertEntry } from "../../session/commit.js";
import { SessionInvariantError } from "../../session/session.js";
import { operationScopeOf, } from "../../session/types.js";
import { branchTip, operationPreparation, setValue } from "../../session/values.js";
import { chainEntries, committedEntryEvents } from "../transcript.js";
import { assistantReadyAtBoundary, boundaryPlacementEvents, finishRunBoundary, planBoundaryInbox, } from "./boundary.js";
import { prepareCompactionThreshold } from "./structural.js";
/** Consume before_run and commit the initial checkpoint. */
export async function startRun(lane, drive, run) {
    const prompt = await lane.continueOperation(run, async (_state, _current, meta, reader) => {
        if (meta.intent.kind !== "run")
            throw new SessionInvariantError("Run operation has non-run intent");
        const entries = await reader.getEntries(meta.intent.promptEntryIds, drive.context);
        const messages = meta.intent.promptEntryIds.map((id) => {
            const entry = entries.get(id);
            if (entry?.type !== "message") {
                throw new SessionInvariantError(`Run prompt entry ${id} is missing its message`);
            }
            return entry.message;
        });
        return { kind: "return", result: messages };
    }, drive.context);
    if (prompt.kind === "cancel_requested")
        return { kind: "continue" };
    const hook = await lane.hooks.runWithGate("before_run", { lane: lane.name, runId: drive.operationId, prompt: prompt.value, resources: lane.readConfig().resources }, drive.gate, drive.context);
    const injected = hook?.messages ?? [];
    for (const message of injected) {
        if (message.role === "assistant" && message.stopReason === "pending") {
            throw new SessionInvariantError("before_run returned a pending assistant message");
        }
    }
    const reserved = injected.map((message) => ({ id: lane.session.idGenerator.next(), message }));
    const result = await lane.continueOperation(run, (state, current) => {
        const entries = chainEntries(state.tipId, reserved.map(({ id, message }) => ({ id, type: "message", message })));
        const triggerEntryId = entries.at(-1)?.id ?? state.tipId;
        if (triggerEntryId === null)
            throw new SessionInvariantError("Run start has no trigger entry");
        const nextState = {
            ...operationScopeOf(current),
            at: "checkpoint",
            continuation: { kind: "need_assistant", overflowRecoveryUsed: false },
            triggerEntryId,
        };
        return {
            kind: "commit",
            writes: [
                ...entries.map((entry) => insertEntry(entry)),
                ...(entries.length === 0 ? [] : [setValue(branchTip(lane.name), triggerEntryId)]),
            ],
            operationState: nextState,
            lane: { tipId: triggerEntryId },
            materialize: () => ({ kind: "continue" }),
            events: (commit) => committedEntryEvents(entries, commit, lane.name, drive.operationId),
        };
    }, drive.context);
    return result.kind === "cancel_requested" ? { kind: "continue" } : result.value;
}
/** Advance one durable run boundary with at most one commit. */
export async function runCheckpoint(lane, drive, run) {
    const threshold = await prepareCompactionThreshold(lane, drive, run);
    if (threshold.kind === "cancel_requested")
        return { kind: "continue" };
    const planned = await lane.continueOperation(run, async (state, current, _meta, reader) => {
        const placement = await planBoundaryInbox(lane, drive, state, current, reader, state.tipId, threshold.value === undefined && current.continuation.kind === "may_finish");
        if (placement.triggerEntryId !== undefined) {
            return {
                kind: "commit",
                writes: placement.writes,
                operationState: assistantReadyAtBoundary(lane, state, current, placement.triggerEntryId, false),
                lane: { tipId: placement.tipId, inbox: placement.inbox },
                materialize: () => ({ kind: "continue" }),
                events: (commit) => boundaryPlacementEvents(placement, commit, 0, lane.name, drive.operationId),
            };
        }
        if (threshold.value !== undefined) {
            const structural = {
                ...operationScopeOf(current),
                at: "summary.deciding",
                task: {
                    taskId: threshold.value.taskId,
                    reason: "threshold",
                    boundary: {
                        kind: "resume_checkpoint",
                        resumeAfter: { continuation: current.continuation, triggerEntryId: current.triggerEntryId },
                    },
                },
            };
            return {
                kind: "commit",
                writes: [
                    ...placement.writes,
                    setValue(operationPreparation(drive.operationId, threshold.value.taskId), threshold.value.preparation),
                ],
                operationState: structural,
                lane: { tipId: placement.tipId, inbox: placement.inbox },
                materialize: () => ({ kind: "continue" }),
                events: (commit) => [
                    ...boundaryPlacementEvents(placement, commit, 0, lane.name, drive.operationId),
                    {
                        type: "compaction_start",
                        lane: lane.name,
                        runId: drive.operationId,
                        reason: "threshold",
                        startedAt: commit.timestamp,
                    },
                ],
            };
        }
        if (current.continuation.kind === "need_assistant") {
            return {
                kind: "commit",
                writes: placement.writes,
                operationState: assistantReadyAtBoundary(lane, state, current, current.triggerEntryId, current.continuation.overflowRecoveryUsed),
                lane: { tipId: placement.tipId, inbox: placement.inbox },
                materialize: () => ({ kind: "continue" }),
                events: (commit) => boundaryPlacementEvents(placement, commit, 0, lane.name, drive.operationId),
            };
        }
        return {
            kind: "return",
            result: { kind: "finish_pending", entryIds: placement.entries.map((entry) => entry.id) },
        };
    }, drive.context);
    if (planned.kind === "cancel_requested")
        return { kind: "continue" };
    if (planned.value.kind !== "finish_pending")
        return planned.value;
    if (run.continuation.kind !== "may_finish") {
        throw new SessionInvariantError("Checkpoint finish mediation requires a finish continuation");
    }
    return finishRunBoundary(lane, drive, run, run.continuation, planned.value.entryIds);
}
//# sourceMappingURL=checkpoint.js.map