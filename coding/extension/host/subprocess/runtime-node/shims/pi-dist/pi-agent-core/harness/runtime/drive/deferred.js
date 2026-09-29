import { getTelemetryContext, withAbortSignal } from "../../context.js";
import { consumeAssistantStream } from "../../execution/assistant.js";
import { applyStreamOptionsPatch } from "../../hooks.js";
import { SessionInvariantError } from "../../session/session.js";
import { operationScopeOf, } from "../../session/types.js";
import { deleteList, pendingAssistantFrames } from "../../session/values.js";
import { openAssistantResponse, publishConfigurationFailure, publishResponse } from "./response.js";
function configurationError(identity) {
    return {
        code: "model_unavailable",
        message: "The configured model is unavailable in this process",
        details: identity,
    };
}
export async function readDeferredSourceHandle(reader, deferred, context) {
    const source = (await reader.getEntries([deferred.sourceEntryId], context)).get(deferred.sourceEntryId);
    if (source?.type !== "message" ||
        source.message.role !== "assistant" ||
        source.message.stopReason !== "deferred" ||
        source.message.deferred === undefined) {
        throw new SessionInvariantError(`Deferred source ${deferred.sourceEntryId} is missing its assistant handle`);
    }
    const handle = source.message.deferred;
    const identity = deferred.configuration.model;
    if (handle.id.length === 0 ||
        handle.provider !== identity.provider ||
        handle.modelId !== identity.modelId ||
        handle.api !== source.message.api) {
        throw new SessionInvariantError(`Deferred source ${deferred.sourceEntryId} has an invalid handle`);
    }
    return handle;
}
async function readSourceHandle(lane, drive, deferred) {
    return lane.continueOperation(deferred, async (_state, _current, _meta, reader) => ({
        kind: "return",
        result: await readDeferredSourceHandle(reader, deferred, drive.context),
    }), drive.context);
}
async function prepareDeferredPoll(lane, drive, expected) {
    const source = await readSourceHandle(lane, drive, expected);
    if (source.kind === "cancel_requested")
        return source;
    if (drive.deferredPermits === 0)
        return { kind: "waiting", source: source.value };
    const identity = expected.configuration.model;
    const model = lane.models.getModel(identity.provider, identity.modelId);
    if (model === undefined)
        return { kind: "configuration_failure" };
    const baseOptions = { ...expected.streamOptions, deferred: false };
    const poll = expected.at === "deferred.suspended" ? expected.poll + 1 : expected.poll;
    const beforeRequest = await lane.hooks.runWithGate("before_request", {
        lane: lane.name,
        runId: drive.operationId,
        model,
        step: "deferred",
        attempt: poll,
        streamOptions: baseOptions,
    }, drive.gate, drive.context);
    return {
        kind: "ready",
        source: source.value,
        model,
        poll,
        streamOptions: {
            ...(beforeRequest?.streamOptions === undefined
                ? baseOptions
                : applyStreamOptionsPatch(baseOptions, beforeRequest.streamOptions)),
            deferred: false,
        },
    };
}
async function publishPollIntent(lane, drive, deferred, prepared, recovery) {
    const at = Date.now();
    const next = {
        at: "deferred.effect_pending",
        stepId: deferred.stepId,
        sourceEntryId: deferred.sourceEntryId,
        poll: prepared.poll,
        responseEntryId: lane.session.idGenerator.next(at),
        usageId: lane.session.idGenerator.next(at),
        configuration: deferred.configuration,
        streamOptions: deferred.streamOptions,
    };
    return lane.continueOperation(deferred, (_state, current) => {
        const nextState = { ...operationScopeOf(current), ...next };
        return {
            kind: "commit",
            writes: deferred.at === "deferred.effect_pending"
                ? [deleteList(pendingAssistantFrames(drive.operationId, deferred.responseEntryId))]
                : [],
            operationState: nextState,
            materialize: () => {
                drive.deferredPermits--;
                return nextState;
            },
            events: () => [
                {
                    type: "run_resume",
                    lane: lane.name,
                    runId: drive.operationId,
                    ...(recovery ? { recovery: true } : {}),
                },
                {
                    type: "turn_start",
                    lane: lane.name,
                    runId: drive.operationId,
                    turnId: `${next.stepId}:poll:${next.poll}`,
                    ...(recovery ? { recovery: true } : {}),
                },
            ],
        };
    }, drive.context);
}
async function performDeferredPoll(lane, drive, prepared, intent, recovery) {
    const response = openAssistantResponse(lane, drive, intent.responseEntryId, recovery);
    let metadata = {};
    const admitted = withAbortSignal(drive.gate.signal, drive.context);
    const stream = drive.gate.admit(() => lane.models.streamDeferred(prepared.model, prepared.source, {
        wait: 0,
        signal: admitted.abortSignal,
        telemetryContext: getTelemetryContext(admitted),
        timeoutMs: prepared.streamOptions.timeoutMs,
        maxRetries: prepared.streamOptions.maxRetries,
        maxRetryDelayMs: prepared.streamOptions.maxRetryDelayMs,
        headers: prepared.streamOptions.headers,
        onPayload: async (payload, requestModel) => {
            const result = await lane.hooks.runWithGate("before_payload", { lane: lane.name, runId: drive.operationId, model: requestModel, payload }, drive.gate, drive.context);
            return result?.payload;
        },
        onResponse: (response) => {
            metadata = { status: response.status, headers: response.headers };
        },
    }));
    try {
        return await consumeAssistantStream(stream, response.observer, (message, context) => response.afterResponse(message, metadata, context), drive.context);
    }
    finally {
        await response.close();
    }
}
async function pollDeferred(lane, drive, expected, recovery) {
    const prepared = await prepareDeferredPoll(lane, drive, expected);
    if (prepared.kind === "cancel_requested")
        return { kind: "continue" };
    if (prepared.kind === "waiting") {
        return {
            kind: "waiting",
            outcome: {
                kind: "waiting",
                operationId: drive.operationId,
                reason: "deferred",
                deferred: prepared.source,
            },
        };
    }
    if (prepared.kind === "configuration_failure") {
        return publishConfigurationFailure(lane, drive, expected, configurationError(expected.configuration.model));
    }
    const intent = await publishPollIntent(lane, drive, expected, prepared, recovery);
    if (intent.kind === "cancel_requested")
        return { kind: "continue" };
    const response = await performDeferredPoll(lane, drive, prepared, intent.value, recovery);
    return publishResponse(lane, drive, intent.value, response, recovery ? { recovery: true } : {});
}
/** Poll one durably suspended deferred response when this pass carries a permit. */
export function runDeferredSuspended(lane, drive, deferred) {
    return pollDeferred(lane, drive, deferred, false);
}
/** Replace one orphaned unknown-outcome poll under fresh ids when this pass carries a permit. */
export function recoverDeferredPoll(lane, drive, deferred) {
    return pollDeferred(lane, drive, deferred, true);
}
/** Advance or report the wait for one deferred run phase. */
export function runDeferred(lane, drive, deferred) {
    return deferred.at === "deferred.suspended"
        ? runDeferredSuspended(lane, drive, deferred)
        : recoverDeferredPoll(lane, drive, deferred);
}
//# sourceMappingURL=deferred.js.map