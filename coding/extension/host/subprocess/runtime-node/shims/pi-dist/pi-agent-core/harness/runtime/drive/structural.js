import { isRetryableAssistantError, retryDelayMs, } from "../../../../pi-ai/sdk-bundle/index.js";
import { generateBranchSummaryWithRequest } from "../../compaction/branch-summarization.js";
import { compactWithRequest, prepareCompaction, shouldCompact } from "../../compaction/compaction.js";
import { getTelemetryContext, withAbortSignal } from "../../context.js";
import { AbortRequested } from "../../execution/effect-gate.js";
import { applyStreamOptionsPatch } from "../../hooks.js";
import { insertEntry, insertUsage } from "../../session/commit.js";
import { SessionInvariantError } from "../../session/session.js";
import { operationScopeOf, } from "../../session/types.js";
import { branchTip, entryLabel, operationPreparation, setValue } from "../../session/values.js";
import { committedEntryEvents, readBoundedEntries } from "../transcript.js";
import { assistantReadyAtBoundary, boundaryPlacementEvents, finishRunBoundary, normalizedRetryPolicy, planBoundaryInbox, } from "./boundary.js";
import { retryNotBefore, waitUntil } from "./retry.js";
import { operationCleanupWrites, operationResultRecord } from "./terminal.js";
class StructuralCancelled extends Error {
    constructor() {
        super("Structural generation was cancelled");
        this.name = "StructuralCancelled";
    }
}
function durableFileOperations(fileOps) {
    return {
        read: [...fileOps.read],
        written: [...fileOps.written],
        edited: [...fileOps.edited],
    };
}
export function durableCompactionPreparation(preparation) {
    return {
        kind: "compaction",
        messagesToSummarize: preparation.messagesToSummarize,
        turnPrefixMessages: preparation.turnPrefixMessages,
        retainedTail: preparation.retainedTail,
        isSplitTurn: preparation.isSplitTurn,
        tokensBefore: preparation.tokensBefore,
        ...(preparation.previousSummary === undefined ? {} : { previousSummary: preparation.previousSummary }),
        fileOps: durableFileOperations(preparation.fileOps),
        settings: preparation.settings,
    };
}
export function durableBranchPreparation(preparation) {
    return {
        kind: "branch_summary",
        messages: preparation.messages,
        fileOps: durableFileOperations(preparation.fileOps),
        totalTokens: preparation.totalTokens,
    };
}
function fileOperations(fileOps) {
    return {
        read: new Set(fileOps.read),
        written: new Set(fileOps.written),
        edited: new Set(fileOps.edited),
    };
}
function compactionPreparation(preparation) {
    return {
        messagesToSummarize: preparation.messagesToSummarize,
        turnPrefixMessages: preparation.turnPrefixMessages,
        retainedTail: preparation.retainedTail,
        isSplitTurn: preparation.isSplitTurn,
        tokensBefore: preparation.tokensBefore,
        ...(preparation.previousSummary === undefined ? {} : { previousSummary: preparation.previousSummary }),
        fileOps: fileOperations(preparation.fileOps),
        settings: preparation.settings,
    };
}
function branchPreparation(preparation) {
    return {
        messages: preparation.messages,
        fileOps: fileOperations(preparation.fileOps),
        totalTokens: preparation.totalTokens,
    };
}
function summaryKind(task) {
    return task.boundary.kind === "commit_navigation" ? "branch_summary" : "compaction";
}
function compactionReason(task) {
    if (task.reason !== undefined)
        return task.reason;
    if (task.boundary.kind === "finish")
        return "manual";
    throw new SessionInvariantError(`In-run compaction task ${task.taskId} is missing its reason`);
}
function navigationBoundary(task) {
    if (task.boundary.kind !== "commit_navigation") {
        throw new SessionInvariantError(`Summary task ${task.taskId} is not a navigation`);
    }
    return task.boundary;
}
async function readStructuralPreparation(lane, drive, deciding) {
    return lane.continueOperation(deciding, async (_state, current, _meta, reader) => {
        const expected = summaryKind(current.task);
        const stored = await reader.getValue(operationPreparation(drive.operationId, current.task.taskId), drive.context);
        if (stored?.value.kind !== expected) {
            throw new SessionInvariantError(`Structural task ${current.task.taskId} is missing its ${expected} preparation`);
        }
        if (current.task.boundary.kind === "commit_navigation") {
            const targetId = current.task.boundary.targetId;
            if (!(await reader.getEntries([targetId], drive.context)).has(targetId)) {
                throw new SessionInvariantError(`Navigation target ${targetId} is missing`);
            }
        }
        return {
            kind: "return",
            result: stored.value.kind === "compaction"
                ? compactionPreparation(stored.value)
                : branchPreparation(stored.value),
        };
    }, drive.context);
}
function summaryContext(lane, resultEntryId, configuration) {
    return {
        resultEntryId,
        configuration,
        streamOptions: { ...lane.readConfig().streamOptions, deferred: false },
        retryPolicy: normalizedRetryPolicy(lane),
    };
}
function usageEvent(row, writeIndex, commit, lane) {
    return {
        type: "usage",
        lane,
        row: { ...row, seq: commit.seqs[writeIndex] },
        totals: commit.stats.usage,
    };
}
function operationError(code, message, details) {
    return { code, message, ...(details === undefined ? {} : { details }) };
}
async function publishStructuralOutcome(lane, drive, capability, outcome) {
    const hookUsageId = (outcome.kind === "compaction" || outcome.kind === "branch_summary") &&
        outcome.fromHook &&
        outcome.result.usage !== undefined
        ? lane.session.idGenerator.next()
        : undefined;
    const published = await lane.continueOperation(capability, async (state, current, meta, reader) => {
        const expected = summaryKind(current.task);
        let terminalCompactionEndedAt;
        if ((outcome.kind === "compaction" || outcome.kind === "branch_summary") && outcome.kind !== expected) {
            throw new SessionInvariantError(`Structural ${outcome.kind} result does not match ${expected} task ${current.task.taskId}`);
        }
        const writes = [];
        const baseEvents = [];
        let terminalTipId = state.tipId;
        if (hookUsageId !== undefined && (outcome.kind === "compaction" || outcome.kind === "branch_summary")) {
            const usage = outcome.result.usage;
            if (usage === undefined)
                throw new SessionInvariantError("Hook usage id exists without structural usage");
            const row = { id: hookUsageId, usage, adjustment: false };
            const writeIndex = writes.length;
            writes.push(insertUsage(row));
            baseEvents.push((commit) => [usageEvent(row, writeIndex, commit, lane.name)]);
        }
        if (outcome.kind === "compaction") {
            const entry = {
                id: outcome.resultEntryId,
                parentId: state.tipId,
                type: "compaction",
                summary: outcome.result.summary,
                retainedTail: outcome.result.retainedTail,
                tokensBefore: outcome.result.tokensBefore,
                ...(outcome.result.details === undefined ? {} : { details: outcome.result.details }),
                ...(outcome.result.usage === undefined ? {} : { usage: outcome.result.usage }),
                fromHook: outcome.fromHook,
            };
            const entryWriteIndex = writes.length;
            writes.push(insertEntry(entry), setValue(branchTip(lane.name), outcome.resultEntryId));
            terminalTipId = outcome.resultEntryId;
            baseEvents.push((commit) => committedEntryEvents([entry], commit, lane.name, drive.operationId, entryWriteIndex));
        }
        else if (outcome.kind === "branch_summary") {
            const boundary = navigationBoundary(current.task);
            const entry = {
                id: outcome.resultEntryId,
                parentId: boundary.targetId,
                type: "branch_summary",
                fromId: meta.sourceTipId,
                summary: outcome.result.summary,
                details: { readFiles: outcome.result.readFiles, modifiedFiles: outcome.result.modifiedFiles },
                ...(outcome.result.usage === undefined ? {} : { usage: outcome.result.usage }),
                fromHook: outcome.fromHook,
            };
            writes.push(setValue(branchTip(lane.name), boundary.targetId));
            const entryWriteIndex = writes.length;
            writes.push(insertEntry(entry), setValue(branchTip(lane.name), outcome.resultEntryId));
            if (boundary.label !== undefined)
                writes.push(setValue(entryLabel(boundary.targetId), boundary.label));
            terminalTipId = outcome.resultEntryId;
            baseEvents.push((commit) => committedEntryEvents([entry], commit, lane.name, drive.operationId, entryWriteIndex));
        }
        const attempt = current.at === "summary.ready"
            ? current.nextAttempt
            : current.at === "summary.effect_pending"
                ? current.attempt
                : undefined;
        if (attempt !== undefined && attempt > 1) {
            baseEvents.push(() => [
                {
                    type: "retry_end",
                    lane: lane.name,
                    runId: drive.operationId,
                    step: current.task.taskId,
                    attempt,
                    success: outcome.kind === "compaction" || outcome.kind === "branch_summary",
                    ...(outcome.kind === "failed" ? { finalError: outcome.error.message } : {}),
                },
            ]);
        }
        if (outcome.kind === "compaction") {
            baseEvents.push((commit) => [
                {
                    type: "compaction_end",
                    lane: lane.name,
                    runId: drive.operationId,
                    reason: compactionReason(current.task),
                    status: "completed",
                    entryId: outcome.resultEntryId,
                    endedAt: terminalCompactionEndedAt ?? commit.timestamp,
                },
            ]);
        }
        const events = (commit) => baseEvents.flatMap((materialize) => materialize(commit));
        switch (current.task.boundary.kind) {
            case "resume_checkpoint": {
                if (outcome.kind === "branch_summary") {
                    throw new SessionInvariantError("Run compaction boundary received a branch summary");
                }
                if (outcome.kind === "compaction" ||
                    (outcome.kind === "declined" && current.task.reason === "threshold")) {
                    if (terminalTipId === null)
                        throw new SessionInvariantError("Run compaction has no Branch tip");
                    const continuation = current.task.boundary.resumeAfter.continuation;
                    const placement = await planBoundaryInbox(lane, drive, state, current, reader, terminalTipId, outcome.kind === "declined" && continuation.kind === "may_finish");
                    if (outcome.kind === "declined" &&
                        placement.triggerEntryId === undefined &&
                        continuation.kind === "may_finish") {
                        return {
                            kind: "return",
                            result: {
                                kind: "finish_pending",
                                entryIds: placement.entries.map((entry) => entry.id),
                            },
                        };
                    }
                    const placementWriteIndex = writes.length;
                    writes.push(...placement.writes);
                    let operationState;
                    if (placement.triggerEntryId !== undefined || continuation.kind === "need_assistant") {
                        const overflowRecoveryUsed = placement.triggerEntryId === undefined && continuation.kind === "need_assistant"
                            ? continuation.overflowRecoveryUsed
                            : false;
                        operationState = assistantReadyAtBoundary(lane, state, current, placement.triggerEntryId ?? current.task.boundary.resumeAfter.triggerEntryId, overflowRecoveryUsed);
                    }
                    else {
                        operationState = {
                            ...operationScopeOf(current),
                            at: "checkpoint",
                            ...current.task.boundary.resumeAfter,
                        };
                    }
                    return {
                        kind: "commit",
                        writes,
                        operationState,
                        lane: { tipId: placement.tipId, inbox: placement.inbox },
                        materialize: () => ({ kind: "continue" }),
                        events: (commit) => [
                            ...(outcome.kind === "compaction"
                                ? events(commit)
                                : [
                                    {
                                        type: "compaction_end",
                                        lane: lane.name,
                                        runId: drive.operationId,
                                        reason: "threshold",
                                        status: "declined",
                                        endedAt: commit.timestamp,
                                    },
                                ]),
                            ...boundaryPlacementEvents(placement, commit, placementWriteIndex, lane.name, drive.operationId),
                        ],
                    };
                }
                if (state.tipId === null)
                    throw new SessionInvariantError("Failed run has no Branch tip");
                const error = outcome.kind === "declined"
                    ? operationError("compaction_declined", "Overflow compaction was declined")
                    : outcome.error;
                const cleanup = await operationCleanupWrites(reader, drive.operationId, current, drive.context);
                const record = operationResultRecord(meta, "failed", state.tipId, error);
                const compactionEnd = outcome.kind === "declined"
                    ? {
                        type: "compaction_end",
                        lane: lane.name,
                        runId: drive.operationId,
                        reason: compactionReason(current.task),
                        status: "declined",
                        endedAt: record.endedAt,
                    }
                    : {
                        type: "compaction_end",
                        lane: lane.name,
                        runId: drive.operationId,
                        reason: compactionReason(current.task),
                        status: "failed",
                        error: outcome.error,
                        endedAt: record.endedAt,
                    };
                return {
                    kind: "finish",
                    writes: [...writes, ...cleanup],
                    record,
                    materialize: () => ({ kind: "settled", outcome: record }),
                    events: (commit) => [
                        ...events(commit),
                        compactionEnd,
                        {
                            type: "run_end",
                            lane: lane.name,
                            runId: drive.operationId,
                            status: "failed",
                            error,
                            fromTipId: meta.sourceTipId,
                            tipId: state.tipId,
                            endedAt: record.endedAt,
                        },
                    ],
                };
            }
            case "finish": {
                if (outcome.kind === "branch_summary") {
                    throw new SessionInvariantError("Compaction finish boundary received a branch summary");
                }
                if (outcome.kind !== "compaction" && state.tipId === null) {
                    throw new SessionInvariantError("Standalone compaction has no Branch tip");
                }
                const error = outcome.kind === "failed" ? outcome.error : undefined;
                const status = outcome.kind === "declined" ? "declined" : outcome.kind === "failed" ? "failed" : "completed";
                const cleanup = await operationCleanupWrites(reader, drive.operationId, current, drive.context);
                const record = operationResultRecord(meta, status, terminalTipId, error);
                terminalCompactionEndedAt = record.endedAt;
                const compactionEnd = outcome.kind === "compaction"
                    ? undefined
                    : outcome.kind === "declined"
                        ? {
                            type: "compaction_end",
                            lane: lane.name,
                            runId: drive.operationId,
                            reason: "manual",
                            status: "declined",
                            endedAt: record.endedAt,
                        }
                        : {
                            type: "compaction_end",
                            lane: lane.name,
                            runId: drive.operationId,
                            reason: "manual",
                            status: "failed",
                            error: outcome.error,
                            endedAt: record.endedAt,
                        };
                return {
                    kind: "finish",
                    writes: [...writes, ...cleanup],
                    record,
                    ...(outcome.kind === "compaction" ? { lane: { tipId: terminalTipId } } : {}),
                    materialize: () => ({ kind: "settled", outcome: record }),
                    events: (commit) => [...events(commit), ...(compactionEnd === undefined ? [] : [compactionEnd])],
                };
            }
            case "commit_navigation": {
                if (outcome.kind === "compaction") {
                    throw new SessionInvariantError("Navigation boundary received a compaction result");
                }
                const error = outcome.kind === "failed" ? outcome.error : undefined;
                const status = outcome.kind === "declined" ? "declined" : outcome.kind === "failed" ? "failed" : "completed";
                const cleanup = await operationCleanupWrites(reader, drive.operationId, current, drive.context);
                const record = operationResultRecord(meta, status, terminalTipId, error);
                const navigationEnd = outcome.kind === "branch_summary"
                    ? {
                        type: "navigation_end",
                        lane: lane.name,
                        runId: drive.operationId,
                        status: "completed",
                        fromTipId: meta.sourceTipId,
                        tipId: terminalTipId,
                        endedAt: record.endedAt,
                    }
                    : outcome.kind === "declined"
                        ? {
                            type: "navigation_end",
                            lane: lane.name,
                            runId: drive.operationId,
                            status: "declined",
                            fromTipId: meta.sourceTipId,
                            tipId: terminalTipId,
                            endedAt: record.endedAt,
                        }
                        : {
                            type: "navigation_end",
                            lane: lane.name,
                            runId: drive.operationId,
                            status: "failed",
                            error: outcome.error,
                            fromTipId: meta.sourceTipId,
                            tipId: terminalTipId,
                            endedAt: record.endedAt,
                        };
                return {
                    kind: "finish",
                    writes: [...writes, ...cleanup],
                    record,
                    ...(outcome.kind === "branch_summary" ? { lane: { tipId: terminalTipId } } : {}),
                    materialize: () => ({ kind: "settled", outcome: record }),
                    events: (commit) => [...events(commit), navigationEnd],
                };
            }
        }
    }, drive.context);
    if (published.kind === "cancel_requested")
        return { kind: "continue" };
    if (published.value.kind !== "finish_pending")
        return published.value;
    const boundary = capability.task.boundary;
    if (boundary.kind !== "resume_checkpoint" || boundary.resumeAfter.continuation.kind !== "may_finish") {
        throw new SessionInvariantError("Structural finish mediation requires a resumable finish boundary");
    }
    return finishRunBoundary(lane, drive, capability, boundary.resumeAfter.continuation, published.value.entryIds, [
        {
            type: "compaction_end",
            lane: lane.name,
            runId: drive.operationId,
            reason: "threshold",
            status: "declined",
            endedAt: Date.now(),
        },
    ]);
}
async function publishStructuralReady(lane, drive, deciding) {
    const resultEntryId = lane.session.idGenerator.next();
    const published = await lane.continueOperation(deciding, (state, current) => {
        const operationState = {
            ...operationScopeOf(current),
            at: "summary.ready",
            task: current.task,
            summaryContext: summaryContext(lane, resultEntryId, state.configuration),
            nextAttempt: 1,
        };
        return {
            kind: "commit",
            writes: [],
            operationState,
            materialize: () => ({ kind: "continue" }),
        };
    }, drive.context);
    return published.kind === "cancel_requested" ? { kind: "continue" } : published.value;
}
/** Consume one durable structural preparation and decision hook. */
export async function runStructuralDecision(lane, drive, deciding) {
    const preparation = await readStructuralPreparation(lane, drive, deciding);
    if (preparation.kind === "cancel_requested")
        return { kind: "continue" };
    if (deciding.task.boundary.kind === "commit_navigation") {
        if (!("messages" in preparation.value)) {
            throw new SessionInvariantError("Navigation task has invalid durable preparation");
        }
        const hook = await lane.hooks.runWithGate("before_navigation", {
            lane: lane.name,
            runId: drive.operationId,
            targetId: deciding.task.boundary.targetId,
            preparation: preparation.value,
            ...(deciding.task.customInstructions === undefined
                ? {}
                : { customInstructions: deciding.task.customInstructions }),
        }, drive.gate, drive.context);
        if (hook?.decline === true)
            return publishStructuralOutcome(lane, drive, deciding, { kind: "declined" });
        if (hook?.summary !== undefined) {
            return publishStructuralOutcome(lane, drive, deciding, {
                kind: "branch_summary",
                resultEntryId: lane.session.idGenerator.next(),
                result: hook.summary,
                fromHook: true,
            });
        }
        return publishStructuralReady(lane, drive, deciding);
    }
    if (!("messagesToSummarize" in preparation.value)) {
        throw new SessionInvariantError("Compaction task has invalid durable preparation");
    }
    const hook = await lane.hooks.runWithGate("before_compaction", {
        lane: lane.name,
        runId: drive.operationId,
        reason: compactionReason(deciding.task),
        preparation: preparation.value,
        ...(deciding.task.customInstructions === undefined
            ? {}
            : { customInstructions: deciding.task.customInstructions }),
    }, drive.gate, drive.context);
    if (hook?.decline === true)
        return publishStructuralOutcome(lane, drive, deciding, { kind: "declined" });
    if (hook?.compaction !== undefined) {
        return publishStructuralOutcome(lane, drive, deciding, {
            kind: "compaction",
            resultEntryId: lane.session.idGenerator.next(),
            result: hook.compaction,
            fromHook: true,
        });
    }
    return publishStructuralReady(lane, drive, deciding);
}
function effectPendingFromReady(ready) {
    return {
        ...operationScopeOf(ready),
        at: "summary.effect_pending",
        task: ready.task,
        summaryContext: ready.summaryContext,
        attempt: ready.nextAttempt,
        usageIds: [],
    };
}
function retryWaitFromEffect(effect, errorMessage) {
    return {
        ...operationScopeOf(effect),
        at: "summary.retry_wait",
        task: effect.task,
        summaryContext: effect.summaryContext,
        nextAttempt: effect.attempt + 1,
        notBefore: retryNotBefore(effect.summaryContext.retryPolicy, effect.attempt),
        errorMessage,
    };
}
function readyFromRetryWait(retry) {
    return {
        ...operationScopeOf(retry),
        at: "summary.ready",
        task: retry.task,
        summaryContext: retry.summaryContext,
        nextAttempt: retry.nextAttempt,
    };
}
async function publishAttemptIntent(lane, drive, ready) {
    return lane.continueOperation(ready, (_state, current) => {
        const effectPending = effectPendingFromReady(current);
        return {
            kind: "commit",
            writes: [],
            operationState: effectPending,
            materialize: () => effectPending,
        };
    }, drive.context);
}
async function publishNestedRequestIntent(lane, drive, effect, index, usageId) {
    return lane.continueOperation(effect, (_state, current) => {
        const next = { ...current, request: { index, usageId } };
        return {
            kind: "commit",
            writes: [],
            operationState: next,
            materialize: () => next,
        };
    }, drive.context);
}
async function publishNestedRequestOutcome(lane, drive, effect, usageId, response) {
    await lane.settleOperation(effect, (_state, current) => {
        const next = { ...current, usageIds: [...current.usageIds, usageId] };
        delete next.request;
        const row = { id: usageId, usage: response.usage, adjustment: false };
        return {
            kind: "commit",
            writes: [insertUsage(row)],
            operationState: next,
            materialize: () => undefined,
            events: (commit) => [usageEvent(row, 0, commit, lane.name)],
        };
    }, drive.context);
}
function requestStreamOptions(options, streamOptions, context, onPayload) {
    return {
        ...options,
        transport: streamOptions.transport,
        timeoutMs: streamOptions.timeoutMs,
        maxRetries: streamOptions.maxRetries,
        maxRetryDelayMs: streamOptions.maxRetryDelayMs,
        headers: streamOptions.headers,
        metadata: streamOptions.metadata,
        cacheRetention: "none",
        deferred: false,
        signal: context.abortSignal,
        telemetryContext: getTelemetryContext(context),
        onPayload,
    };
}
async function performStructuralAttempt(lane, drive, effect, model, preparation) {
    let requestIndex = 0;
    let lastResponse;
    const request = async (aiContext, options, requestContext) => {
        const baseOptions = { ...effect.summaryContext.streamOptions, deferred: false };
        const beforeRequest = await lane.hooks
            .runWithGate("before_request", {
            lane: lane.name,
            runId: drive.operationId,
            model,
            step: summaryKind(effect.task),
            attempt: effect.attempt,
            streamOptions: baseOptions,
        }, drive.gate, requestContext)
            .catch(async (error) => {
            if (!(error instanceof AbortRequested))
                throw error;
            await error.cancellation;
            throw new StructuralCancelled();
        });
        const streamOptions = {
            ...(beforeRequest?.streamOptions === undefined
                ? baseOptions
                : applyStreamOptionsPatch(baseOptions, beforeRequest.streamOptions)),
            deferred: false,
        };
        const usageId = lane.session.idGenerator.next();
        const intent = await publishNestedRequestIntent(lane, drive, effect, requestIndex, usageId);
        requestIndex += 1;
        if (intent.kind === "cancel_requested")
            throw new StructuralCancelled();
        const admittedContext = withAbortSignal(drive.gate.signal, requestContext);
        let response;
        try {
            response = await drive.gate.admit(() => lane.models.completeSimple(model, aiContext, requestStreamOptions(options, streamOptions, admittedContext, async (payload, requestModel) => {
                const hook = await lane.hooks.runWithGate("before_payload", { lane: lane.name, runId: drive.operationId, model: requestModel, payload }, drive.gate, admittedContext);
                return hook?.payload;
            })));
        }
        catch (error) {
            if (!(error instanceof AbortRequested))
                throw error;
            await error.cancellation;
            throw new StructuralCancelled();
        }
        lastResponse = response;
        await publishNestedRequestOutcome(lane, drive, intent.value, usageId, response);
        return response;
    };
    try {
        if (summaryKind(effect.task) === "compaction") {
            if (!("messagesToSummarize" in preparation)) {
                throw new SessionInvariantError("Compaction summary has invalid durable preparation");
            }
            const result = await compactWithRequest(preparation, {
                model,
                customInstructions: effect.task.customInstructions,
                thinkingLevel: effect.summaryContext.configuration.thinkingLevel,
            }, request, drive.context);
            if (!result.ok) {
                return {
                    kind: "error",
                    error: operationError(result.error.code, result.error.message),
                    retryable: lastResponse !== undefined && isRetryableAssistantError(lastResponse),
                };
            }
            return {
                kind: "compaction",
                result: result.value,
                retryable: lastResponse !== undefined && isRetryableAssistantError(lastResponse),
            };
        }
        if (!("messages" in preparation)) {
            throw new SessionInvariantError("Branch summary has invalid durable preparation");
        }
        const result = await generateBranchSummaryWithRequest(preparation, { customInstructions: effect.task.customInstructions }, request, drive.context);
        if (!result.ok) {
            return {
                kind: "error",
                error: operationError(result.error.code, result.error.message),
                retryable: lastResponse !== undefined && isRetryableAssistantError(lastResponse),
            };
        }
        return {
            kind: "branch_summary",
            result: result.value,
            retryable: lastResponse !== undefined && isRetryableAssistantError(lastResponse),
        };
    }
    catch (error) {
        if (error instanceof StructuralCancelled)
            return { kind: "cancel_requested" };
        throw error;
    }
}
async function readAttemptPreparation(lane, drive, ready) {
    return lane.continueOperation(ready, async (_state, current, _meta, reader) => {
        const expected = summaryKind(current.task);
        const stored = await reader.getValue(operationPreparation(drive.operationId, current.task.taskId), drive.context);
        if (stored === undefined || stored.value.kind !== expected) {
            throw new SessionInvariantError(`Structural task ${current.task.taskId} has invalid durable preparation`);
        }
        return {
            kind: "return",
            result: stored.value.kind === "compaction"
                ? compactionPreparation(stored.value)
                : branchPreparation(stored.value),
        };
    }, drive.context);
}
async function publishAttemptResult(lane, drive, effect, result) {
    if (result.kind === "cancel_requested")
        return { kind: "continue" };
    if (result.kind === "compaction") {
        return publishStructuralOutcome(lane, drive, effect, {
            kind: "compaction",
            resultEntryId: effect.summaryContext.resultEntryId,
            result: result.result,
            fromHook: false,
        });
    }
    if (result.kind === "branch_summary") {
        return publishStructuralOutcome(lane, drive, effect, {
            kind: "branch_summary",
            resultEntryId: effect.summaryContext.resultEntryId,
            result: result.result,
            fromHook: false,
        });
    }
    if (result.error.code === "aborted" && lane.state.operation.state.control.status === "running") {
        throw new SessionInvariantError("Structural provider response is aborted while durable control is running");
    }
    if (result.retryable && effect.attempt < effect.summaryContext.retryPolicy.maxAttempts) {
        const retryWait = retryWaitFromEffect(effect, result.error.message);
        const published = await lane.continueOperation(effect, () => ({
            kind: "commit",
            writes: [],
            operationState: retryWait,
            materialize: () => ({ kind: "continue" }),
            events: () => [
                {
                    type: "retry_scheduled",
                    lane: lane.name,
                    runId: drive.operationId,
                    step: effect.task.taskId,
                    attempt: retryWait.nextAttempt,
                    maxAttempts: effect.summaryContext.retryPolicy.maxAttempts,
                    delayMs: retryDelayMs(effect.summaryContext.retryPolicy, effect.attempt),
                    notBefore: retryWait.notBefore,
                    errorMessage: result.error.message,
                },
            ],
        }), drive.context);
        return published.kind === "cancel_requested" ? { kind: "continue" } : published.value;
    }
    return publishStructuralOutcome(lane, drive, effect, { kind: "failed", error: result.error });
}
/** Execute one ready structural generation attempt. */
export async function runStructuralGeneration(lane, drive, ready) {
    const preparation = await readAttemptPreparation(lane, drive, ready);
    if (preparation.kind === "cancel_requested")
        return { kind: "continue" };
    const identity = ready.summaryContext.configuration.model;
    const model = lane.models.getModel(identity.provider, identity.modelId);
    if (model === undefined) {
        return publishStructuralOutcome(lane, drive, ready, {
            kind: "failed",
            error: operationError("model_unavailable", "The configured model is unavailable in this process", identity),
        });
    }
    const intent = await publishAttemptIntent(lane, drive, ready);
    if (intent.kind === "cancel_requested")
        return { kind: "continue" };
    const result = await performStructuralAttempt(lane, drive, intent.value, model, preparation.value);
    return publishAttemptResult(lane, drive, intent.value, result);
}
/** Consume one structural retry wait without starting a provider effect. */
export async function runStructuralRetryWait(lane, drive, retry) {
    if (Date.now() < retry.notBefore) {
        if (!drive.waitForRetry) {
            return {
                kind: "waiting",
                outcome: {
                    kind: "waiting",
                    operationId: drive.operationId,
                    reason: "retry",
                    notBefore: retry.notBefore,
                },
            };
        }
        await drive.gate.admit(() => waitUntil(retry.notBefore, drive.gate.signal));
    }
    const published = await lane.continueOperation(retry, (_state, current) => {
        const ready = readyFromRetryWait(current);
        return {
            kind: "commit",
            writes: [],
            operationState: ready,
            materialize: () => ({ kind: "continue" }),
            events: () => [
                {
                    type: "retry_start",
                    lane: lane.name,
                    runId: drive.operationId,
                    step: current.task.taskId,
                    attempt: current.nextAttempt,
                },
            ],
        };
    }, drive.context);
    return published.kind === "cancel_requested" ? { kind: "continue" } : published.value;
}
/** Convert an orphaned structural attempt into a fresh numbered attempt or terminal failure. */
export async function recoverStructuralGeneration(lane, drive, effect) {
    const error = operationError("structural_interrupted", "Structural summary attempt was interrupted and its external outcome is unknown");
    if (effect.attempt >= effect.summaryContext.retryPolicy.maxAttempts) {
        return publishStructuralOutcome(lane, drive, effect, { kind: "failed", error });
    }
    const retryWait = retryWaitFromEffect(effect, error.message);
    const published = await lane.continueOperation(effect, () => ({
        kind: "commit",
        writes: [],
        operationState: retryWait,
        materialize: () => ({ kind: "continue" }),
        events: () => [
            {
                type: "retry_scheduled",
                lane: lane.name,
                runId: drive.operationId,
                step: effect.task.taskId,
                attempt: retryWait.nextAttempt,
                maxAttempts: effect.summaryContext.retryPolicy.maxAttempts,
                delayMs: retryDelayMs(effect.summaryContext.retryPolicy, effect.attempt),
                notBefore: retryWait.notBefore,
                errorMessage: error.message,
                recovery: true,
            },
        ],
    }), drive.context);
    return published.kind === "cancel_requested" ? { kind: "continue" } : published.value;
}
/** Prepare threshold compaction only when no newer compaction already guards this trigger. */
export async function prepareCompactionThreshold(lane, drive, checkpoint) {
    const settings = checkpoint.settings.compaction;
    const identity = lane.state.configuration.model;
    const model = lane.models.getModel(identity.provider, identity.modelId);
    if (!settings.enabled || model === undefined)
        return { kind: "result", value: undefined };
    const path = await readBoundedEntries(lane, drive, checkpoint);
    if (path.kind === "cancel_requested")
        return path;
    const triggerIndex = path.value.findIndex((entry) => entry.id === checkpoint.triggerEntryId);
    let newestCompactionIndex = -1;
    for (let index = path.value.length - 1; index >= 0; index--) {
        if (path.value[index].type === "compaction") {
            newestCompactionIndex = index;
            break;
        }
    }
    if (newestCompactionIndex >= triggerIndex && newestCompactionIndex !== -1) {
        return { kind: "result", value: undefined };
    }
    if (triggerIndex === -1) {
        throw new SessionInvariantError(`Checkpoint trigger ${checkpoint.triggerEntryId} is missing from its Branch`);
    }
    const prepared = prepareCompaction(path.value, settings);
    if (!prepared.ok)
        throw prepared.error;
    if (prepared.value === undefined || !shouldCompact(prepared.value.tokensBefore, model.contextWindow, settings)) {
        return { kind: "result", value: undefined };
    }
    return {
        kind: "result",
        value: { taskId: lane.session.idGenerator.next(), preparation: durableCompactionPreparation(prepared.value) },
    };
}
/** Prepare one overflow compaction before the response settlement transaction. */
export async function prepareOverflowCompaction(lane, drive, generation) {
    if (generation.generationContext.overflowRecoveryUsed)
        return undefined;
    const path = await readBoundedEntries(lane, drive, generation);
    if (path.kind === "cancel_requested")
        return undefined;
    const prepared = prepareCompaction(path.value, generation.settings.compaction);
    if (!prepared.ok)
        throw prepared.error;
    if (prepared.value === undefined)
        return undefined;
    return {
        taskId: lane.session.idGenerator.next(),
        preparation: durableCompactionPreparation(prepared.value),
    };
}
/** Atomically move an unsummarized navigation and finish its operation. */
export function commitNavigation(lane, drive, navigation) {
    return lane
        .continueOperation(navigation, async (_state, current, meta, reader) => {
        if (current.targetId !== null &&
            !(await reader.getEntries([current.targetId], drive.context)).has(current.targetId)) {
            throw new SessionInvariantError(`Navigation target ${current.targetId} is missing`);
        }
        if (current.targetId === meta.sourceTipId) {
            throw new SessionInvariantError("Navigation target must differ from its source tip");
        }
        if (current.targetId === null && current.label !== undefined) {
            throw new SessionInvariantError("Root navigation cannot set a label");
        }
        const writes = [setValue(branchTip(lane.name), current.targetId)];
        if (current.label !== undefined && current.targetId !== null) {
            writes.push(setValue(entryLabel(current.targetId), current.label));
        }
        const cleanup = await operationCleanupWrites(reader, drive.operationId, current, drive.context);
        const record = operationResultRecord(meta, "completed", current.targetId);
        return {
            kind: "finish",
            writes: [...writes, ...cleanup],
            record,
            lane: { tipId: current.targetId },
            materialize: () => ({ kind: "settled", outcome: record }),
            events: () => [
                {
                    type: "navigation_end",
                    lane: lane.name,
                    runId: drive.operationId,
                    status: "completed",
                    fromTipId: meta.sourceTipId,
                    tipId: current.targetId,
                    endedAt: record.endedAt,
                },
            ],
        };
    }, drive.context)
        .then((result) => (result.kind === "cancel_requested" ? { kind: "continue" } : result.value));
}
//# sourceMappingURL=structural.js.map