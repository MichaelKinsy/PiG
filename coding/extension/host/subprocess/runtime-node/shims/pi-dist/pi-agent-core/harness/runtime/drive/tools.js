import { AbortRequested } from "../../execution/effect-gate.js";
import { applyBeforeToolDecision, createToolResultMessage, executeToolCall, finalizeToolCall, prepareToolCall, toolResultFromMessage, } from "../../execution/tools.js";
import { SessionInvariantError } from "../../session/session.js";
import { deleteValue, operationToolArgs, operationToolMemo, operationToolMemoPrefix, pendingEntry, pendingToolOutput, setValue, } from "../../session/values.js";
import { openToolProgress } from "../progress.js";
import { materializeReady, readToolBatchSource, toolCallFor, withToolBatch, } from "./tool-placement.js";
const INTERRUPTION_MARKER = "[Tool execution was interrupted. The preceding output is the latest durable progress snapshot; newer live output may be missing, and the external outcome is unknown.]";
class ToolInvocationEnded extends Error {
    constructor() {
        super("Tool invocation no longer owns its durable effect");
        this.name = "ToolInvocationEnded";
    }
}
function currentBatch(lane) {
    const operation = lane.state.operation;
    if (operation?.state.at !== "tools")
        return undefined;
    return { run: operation.state, batch: operation.state.batch };
}
function findCall(batch, sourceIndex, resultEntryId) {
    return batch.calls.find((call) => call.sourceIndex === sourceIndex && call.resultEntryId === resultEntryId);
}
function replaceCall(batch, replacement) {
    return {
        ...batch,
        calls: batch.calls.map((call) => call.sourceIndex === replacement.sourceIndex && call.resultEntryId === replacement.resultEntryId
            ? replacement
            : call),
    };
}
function validateMemoName(name) {
    if (name.length === 0)
        throw new TypeError("Tool invocation memo name must not be empty");
    if (name.includes(":"))
        throw new TypeError("Tool invocation memo name must not contain ':'");
}
function invocationCapability(lane, drive, batch, call) {
    let active = true;
    const ownsEffect = (state) => {
        const operation = state.operation;
        if (operation?.state.at !== "tools")
            return false;
        return findCall(operation.state.batch, call.sourceIndex, call.resultEntryId)?.status === "effect_pending";
    };
    const ended = () => new ToolInvocationEnded();
    return {
        invocation: {
            invocationId: call.resultEntryId,
            operationId: drive.operationId,
            turnId: batch.turnId,
            getMemo(name) {
                validateMemoName(name);
                if (!active)
                    return Promise.reject(ended());
                return lane.command(async (state, reader) => {
                    if (!ownsEffect(state))
                        return { kind: "reject", error: ended() };
                    const stored = await reader.getValue(operationToolMemo(drive.operationId, call.resultEntryId, name), drive.context);
                    return { kind: "return", result: stored?.value };
                }, drive.context);
            },
            setMemo(name, value) {
                validateMemoName(name);
                if (!active)
                    return Promise.reject(ended());
                return lane.command((state) => {
                    if (!ownsEffect(state))
                        return { kind: "reject", error: ended() };
                    const address = operationToolMemo(drive.operationId, call.resultEntryId, name);
                    return {
                        kind: "commit",
                        writes: [value === undefined ? deleteValue(address) : setValue(address, value)],
                        next: state,
                        materialize: () => undefined,
                    };
                }, drive.context);
            },
        },
        expire() {
            active = false;
        },
    };
}
function syntheticMessage(toolCall, content, options = {}) {
    return {
        role: "toolResult",
        toolCallId: toolCall.id,
        toolName: toolCall.name,
        content,
        ...(options.details === undefined ? {} : { details: options.details }),
        ...(options.usage === undefined ? {} : { usage: options.usage }),
        isError: true,
        timestamp: Date.now(),
    };
}
function abortedOutcome(toolCall) {
    return {
        toolCall,
        message: syntheticMessage(toolCall, [{ type: "text", text: "Tool execution was cancelled before completion." }]),
        terminate: false,
    };
}
function interruptedOutcome(toolCall, checkpoint) {
    return {
        toolCall,
        message: syntheticMessage(toolCall, [...(checkpoint?.content ?? []), { type: "text", text: INTERRUPTION_MARKER }], checkpoint === undefined ? {} : { details: checkpoint.details, usage: checkpoint.usage }),
        terminate: false,
    };
}
function truncatedOutcome(toolCall) {
    return {
        toolCall,
        message: syntheticMessage(toolCall, [
            {
                type: "text",
                text: `Tool call ${JSON.stringify(toolCall.name)} was not executed because the assistant response hit the output token limit, so its arguments may be truncated. Re-issue the tool call with complete arguments.`,
            },
        ]),
        terminate: false,
    };
}
function outcomeFromFinalizedCall(finalized) {
    return { toolCall: finalized.toolCall, message: createToolResultMessage(finalized), terminate: finalized.terminate };
}
async function publishToolIntent(lane, drive, run, planned, toolCall, args, replay, recovery) {
    return lane.continueOperation(run, (_state, run) => {
        const effectPending = {
            status: "effect_pending",
            sourceIndex: planned.sourceIndex,
            resultEntryId: planned.resultEntryId,
            replay,
        };
        return {
            kind: "commit",
            writes: [setValue(operationToolArgs(drive.operationId, run.batch.turnId, planned.sourceIndex), args)],
            operationState: withToolBatch(run, replaceCall(run.batch, effectPending)),
            materialize: () => effectPending,
            events: () => [
                {
                    type: "tool_start",
                    lane: lane.name,
                    runId: drive.operationId,
                    turnId: run.batch.turnId,
                    toolCallId: toolCall.id,
                    toolName: toolCall.name,
                    args,
                    ...(recovery ? { recovery: true } : {}),
                },
            ],
        };
    }, drive.context);
}
async function publishToolOutcome(lane, drive, capability, call, finalized, recovery) {
    await lane.settleOperation(capability, async (_state, run, _meta, reader) => {
        const { toolCall } = finalized;
        const memos = await reader.scanValues(operationToolMemoPrefix(drive.operationId, call.resultEntryId), drive.context);
        const durableTerminate = run.control.status === "running" && finalized.terminate;
        const outcome = {
            status: "outcome_ready",
            sourceIndex: call.sourceIndex,
            resultEntryId: call.resultEntryId,
            terminate: durableTerminate,
        };
        return {
            kind: "commit",
            writes: [
                setValue(pendingEntry(call.resultEntryId), { type: "message", payload: finalized.message }),
                deleteValue(pendingToolOutput(drive.operationId, call.resultEntryId)),
                ...memos.map(({ address }) => deleteValue(address)),
            ],
            operationState: withToolBatch(run, replaceCall(run.batch, outcome)),
            materialize: () => undefined,
            events: () => [
                ...(call.status === "planned"
                    ? [
                        {
                            type: "tool_start",
                            lane: lane.name,
                            runId: drive.operationId,
                            turnId: run.batch.turnId,
                            toolCallId: toolCall.id,
                            toolName: toolCall.name,
                            args: toolCall.arguments,
                            ...(recovery ? { recovery: true } : {}),
                        },
                    ]
                    : []),
                {
                    type: "tool_end",
                    lane: lane.name,
                    runId: drive.operationId,
                    turnId: run.batch.turnId,
                    toolCallId: toolCall.id,
                    toolName: toolCall.name,
                    result: toolResultFromMessage(finalized.message, durableTerminate),
                    isError: finalized.message.isError,
                    terminate: durableTerminate,
                    ...(recovery ? { recovery: true } : {}),
                },
            ],
        };
    }, drive.context);
}
async function clearReplayCheckpoint(lane, drive, batch, call, toolCall) {
    return lane.command(async (state, reader) => {
        const stored = await reader.getValue(operationToolArgs(drive.operationId, batch.turnId, call.sourceIndex), drive.context);
        if (stored === undefined) {
            throw new SessionInvariantError(`Tool call ${call.resultEntryId} is missing persisted arguments`);
        }
        return {
            kind: "commit",
            writes: [deleteValue(pendingToolOutput(drive.operationId, call.resultEntryId))],
            next: state,
            materialize: () => stored.value,
            events: () => [
                {
                    type: "tool_start",
                    lane: lane.name,
                    runId: drive.operationId,
                    turnId: batch.turnId,
                    toolCallId: toolCall.id,
                    toolName: toolCall.name,
                    args: stored.value,
                    recovery: true,
                },
            ],
        };
    }, drive.context);
}
function readCheckpoint(lane, drive, call) {
    return lane.command(async (_state, reader) => {
        const stored = await reader.getValue(pendingToolOutput(drive.operationId, call.resultEntryId), drive.context);
        return { kind: "return", result: stored?.value };
    }, drive.context);
}
async function resolveToolContext(lane, drive) {
    const source = lane.readConfig().toolContext;
    return (typeof source === "function" ? await source(drive.context) : source);
}
async function performToolInvocation(lane, drive, batch, call, cleared, toolContext, recovery) {
    const capability = invocationCapability(lane, drive, batch, call);
    const progress = openToolProgress(lane, drive, batch.turnId, call.sourceIndex, call.resultEntryId);
    let latestUpdateDelivery = Promise.resolve();
    const publishUpdate = (partial) => {
        latestUpdateDelivery = lane.emitBatch([
            {
                type: "tool_update",
                lane: lane.name,
                runId: drive.operationId,
                turnId: batch.turnId,
                toolCallId: cleared.toolCall.id,
                toolName: cleared.toolCall.name,
                partialResult: partial,
                ...(recovery ? { recovery: true } : {}),
            },
        ], drive.context);
        void latestUpdateDelivery.catch(() => { });
    };
    let execution;
    try {
        execution = executeToolCall(cleared, drive.gate, (partial, options) => {
            publishUpdate(partial);
            if (options?.checkpoint === true)
                progress.write(partial);
        }, toolContext, capability.invocation, drive.context);
    }
    catch (error) {
        capability.expire();
        progress.seal();
        await progress.drain();
        if (!(error instanceof AbortRequested))
            throw error;
        await error.cancellation;
        return recovery ? interruptedOutcome(cleared.toolCall, undefined) : abortedOutcome(cleared.toolCall);
    }
    const executed = await execution.finally(() => {
        capability.expire();
        progress.seal();
    });
    await latestUpdateDelivery;
    await progress.drain();
    let patch;
    try {
        patch = await lane.hooks.runToolWithGate("after_tool", {
            lane: lane.name,
            runId: drive.operationId,
            toolCallId: cleared.toolCall.id,
            toolName: cleared.toolCall.name,
            args: cleared.args,
            content: executed.result.content,
            ...(executed.result.details === undefined ? {} : { details: executed.result.details }),
            isError: executed.isError,
            ...(executed.result.usage === undefined ? {} : { usage: executed.result.usage }),
        }, drive.gate, drive.context);
    }
    catch (error) {
        if (!(error instanceof AbortRequested))
            throw error;
        patch = undefined;
    }
    const finalized = finalizeToolCall(cleared, executed, patch);
    return { toolCall: finalized.toolCall, message: createToolResultMessage(finalized), terminate: finalized.terminate };
}
async function prepareToolInvocation(lane, drive, sources, call, tools) {
    const toolCall = toolCallFor(sources, call);
    if (sources.assistant.stopReason === "length") {
        return { kind: "outcome", outcome: truncatedOutcome(toolCall) };
    }
    const prepared = prepareToolCall(toolCall, tools);
    if ("kind" in prepared)
        return { kind: "outcome", outcome: outcomeFromFinalizedCall(prepared) };
    let decision;
    try {
        decision = await lane.hooks.runToolWithGate("before_tool", {
            lane: lane.name,
            runId: drive.operationId,
            toolCallId: toolCall.id,
            toolName: toolCall.name,
            args: prepared.args,
        }, drive.gate, drive.context);
    }
    catch (error) {
        if (!(error instanceof AbortRequested))
            throw error;
        await error.cancellation;
        return { kind: "outcome", outcome: abortedOutcome(toolCall) };
    }
    const cleared = applyBeforeToolDecision(prepared, decision);
    return "kind" in cleared
        ? { kind: "outcome", outcome: outcomeFromFinalizedCall(cleared) }
        : { kind: "ready", cleared };
}
async function startToolInvocation(lane, drive, run, sources, call, tools, toolContext, recovery) {
    const prepared = await prepareToolInvocation(lane, drive, sources, call, tools);
    if (prepared.kind === "outcome") {
        return { completion: publishToolOutcome(lane, drive, run, call, prepared.outcome, recovery) };
    }
    const effectPending = await publishToolIntent(lane, drive, run, call, prepared.cleared.toolCall, prepared.cleared.args, prepared.cleared.tool.replay ?? "never", recovery);
    return {
        completion: effectPending.kind === "cancel_requested"
            ? publishToolOutcome(lane, drive, run, call, abortedOutcome(prepared.cleared.toolCall), recovery)
            : performToolInvocation(lane, drive, run.batch, effectPending.value, prepared.cleared, toolContext, recovery).then((outcome) => publishToolOutcome(lane, drive, run, effectPending.value, outcome, recovery)),
    };
}
async function recoverToolInvocation(lane, drive, run, sources, call, toolsByName, toolContext, cancelled) {
    const toolCall = toolCallFor(sources, call);
    const tool = toolsByName.get(toolCall.name);
    if (!cancelled && call.replay === "safe" && tool?.replay === "safe") {
        const args = await clearReplayCheckpoint(lane, drive, run.batch, call, toolCall);
        const cleared = { toolCall, tool, args };
        return {
            completion: performToolInvocation(lane, drive, run.batch, call, cleared, toolContext, true).then((outcome) => publishToolOutcome(lane, drive, run, call, outcome, true)),
        };
    }
    const checkpoint = await readCheckpoint(lane, drive, call);
    return {
        completion: publishToolOutcome(lane, drive, run, call, interruptedOutcome(toolCall, checkpoint), true),
    };
}
async function runSequential(lane, drive, run, sources, execution, recovery) {
    const { batch } = run;
    for (let transition = 0; transition <= batch.calls.length * 2 + 1; transition += 1) {
        await materializeReady(lane, drive, run, sources, recovery);
        const current = currentBatch(lane);
        if (current === undefined)
            return { kind: "continue" };
        const call = current.batch.calls.find((candidate) => candidate.status !== "completed");
        if (call === undefined)
            throw new SessionInvariantError("Tool batch remained open after every call completed");
        if (call.status === "outcome_ready")
            throw new SessionInvariantError("Ready tool outcome was not materialized");
        if (current.run.control.status === "cancel_requested") {
            const toolCall = toolCallFor(sources, call);
            if (call.status === "planned") {
                await publishToolOutcome(lane, drive, current.run, call, abortedOutcome(toolCall), recovery);
            }
            else {
                const checkpoint = await readCheckpoint(lane, drive, call);
                await publishToolOutcome(lane, drive, current.run, call, interruptedOutcome(toolCall, checkpoint), recovery);
            }
            continue;
        }
        if (execution === undefined)
            throw new SessionInvariantError("Running tool batch is missing execution context");
        const started = call.status === "planned"
            ? await startToolInvocation(lane, drive, current.run, sources, call, execution.tools, execution.toolContext, recovery)
            : await recoverToolInvocation(lane, drive, current.run, sources, call, execution.toolsByName, execution.toolContext, false);
        await started.completion;
    }
    throw new SessionInvariantError("Sequential tool batch exceeded its bounded transition count");
}
async function runParallel(lane, drive, run, sources, tools, toolsByName, toolContext, recovery) {
    const { batch } = run;
    let materialization = Promise.resolve();
    const scheduleMaterialization = () => {
        const scheduled = materialization.then(() => materializeReady(lane, drive, run, sources, recovery));
        materialization = scheduled.catch(() => { });
        return scheduled;
    };
    const jobs = [];
    for (const call of batch.calls) {
        if (call.status === "completed" || call.status === "outcome_ready")
            continue;
        const started = call.status === "planned"
            ? await startToolInvocation(lane, drive, run, sources, call, tools, toolContext, recovery)
            : await recoverToolInvocation(lane, drive, run, sources, call, toolsByName, toolContext, lane.state.operation.state.control.status === "cancel_requested");
        const job = started.completion.then(async () => {
            await scheduleMaterialization();
        });
        void job.catch(() => { });
        jobs.push(job);
    }
    await Promise.all(jobs);
    await scheduleMaterialization();
    return { kind: "continue" };
}
/** Execute, recover, stage, and source-order one complete durable tool batch. */
export async function runTools(lane, drive, run) {
    const batch = run.batch;
    const recovery = batch.calls.some((call) => call.status === "effect_pending" || call.status === "outcome_ready");
    if (recovery) {
        await lane.emitBatch([
            {
                type: "turn_start",
                lane: lane.name,
                runId: drive.operationId,
                turnId: batch.turnId,
                recovery: true,
            },
        ], drive.context);
    }
    const sources = await readToolBatchSource(lane, drive, batch);
    await materializeReady(lane, drive, run, sources, recovery);
    const current = currentBatch(lane);
    if (current === undefined)
        return { kind: "continue" };
    if (current.run.control.status === "cancel_requested") {
        return runSequential(lane, drive, current.run, sources, undefined, recovery);
    }
    const config = lane.readConfig();
    const active = new Set(batch.configuration.activeToolNames);
    const tools = config.tools.filter((tool) => active.has(tool.name));
    const toolsByName = new Map(tools.map((tool) => [tool.name, tool]));
    const toolContext = await resolveToolContext(lane, drive);
    return run.settings.toolExecution === "sequential"
        ? runSequential(lane, drive, current.run, sources, { tools, toolsByName, toolContext }, recovery)
        : runParallel(lane, drive, current.run, sources, tools, toolsByName, toolContext, recovery);
}
//# sourceMappingURL=tools.js.map