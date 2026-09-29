import { DEFAULT_COMPACTION_SETTINGS } from "../compaction/compaction.js";
import { DEFAULT_RETRY_POLICY, validateCompactionSettings, validateRetryPolicy, validateToolNames } from "../config.js";
import { HarnessEventBus } from "../events.js";
import { HookRegistry } from "../hooks.js";
import { convertToLlm } from "../messages.js";
import { HarnessClosed, HarnessFault, InvalidLane, UnknownTarget } from "../result.js";
import { SessionInvariantError } from "../session/session.js";
import { branchTip, deleteValue, entryLabel, laneConfig, laneState, sessionName, setValue } from "../session/values.js";
import { Lane } from "./lane.js";
import { readLaneStorage, restoreLaneState, restoreSession } from "./restore.js";
import { SliceNotImplemented } from "./types.js";
/** Runtime implementation of AgentHarness. The harness manages lanes but is not itself a lane. */
export class Harness {
    session;
    models;
    hooks;
    events;
    lanesByName = new Map();
    seed;
    configStore;
    closePromise;
    closedError;
    faultError;
    constructor(options, seed, restored) {
        this.session = options.session;
        this.models = options.models;
        this.seed = seed;
        this.events = new HarnessEventBus();
        this.hooks = new HookRegistry((error, hook, lane, context) => this.events.emit({
            type: "handler_error",
            kind: "hook",
            hook,
            error: error.message,
            ...(error.stack === undefined ? {} : { stack: error.stack }),
            lane,
        }, context));
        this.configStore = {
            value: {
                tools: options.tools ?? [],
                resources: options.resources ?? {},
                streamOptions: options.streamOptions ?? {},
                retryPolicy: options.retry ?? DEFAULT_RETRY_POLICY,
                compaction: options.compaction ?? DEFAULT_COMPACTION_SETTINGS,
                steeringMode: options.steeringMode ?? "all",
                followUpMode: options.followUpMode ?? "all",
                toolExecution: options.toolExecution ?? "parallel",
                toolContext: options.toolContext,
                systemPrompt: options.systemPrompt,
                toProviderMessages: options.toProviderMessages ?? ((messages) => convertToLlm(messages)),
                entryProjectors: options.entryProjectors ?? {},
            },
        };
        for (const [name, state] of restored)
            this.lanesByName.set(name, this.buildLane(name, state));
    }
    async lane(name, optionsOrContext, maybeContext) {
        this.assertOpen();
        if (name.length === 0 || name.includes("\u0000")) {
            const reason = name.length === 0 ? "lane name must not be empty" : "lane name must not contain \\u0000";
            throw new InvalidLane({
                lane: name,
                reason,
                message: `Invalid lane ${JSON.stringify(name)}: ${reason}`,
            });
        }
        const options = maybeContext === undefined ? {} : optionsOrContext;
        const context = maybeContext ?? optionsOrContext;
        let lane;
        let delivery;
        try {
            await this.session.mutate(async (mutator) => {
                this.assertOpen();
                lane = this.lanesByName.get(name);
                if (lane !== undefined)
                    return;
                const stored = await readLaneStorage(mutator, name, context);
                if (stored.kind === "lane") {
                    const restored = await restoreLaneState(mutator, name, stored, context);
                    lane = this.buildLane(name, restored);
                    this.lanesByName.set(name, lane);
                    return;
                }
                const tipId = stored.kind === "branch" ? stored.tip.value : (options.createAt ?? null);
                if (stored.kind === "absent" &&
                    tipId !== null &&
                    !(await mutator.getEntries([tipId], context)).has(tipId)) {
                    throw new UnknownTarget({
                        targetId: tipId,
                        message: `Unknown target: ${tipId}`,
                    });
                }
                const attachedConfiguration = {
                    model: { ...this.seed.model },
                    thinkingLevel: this.seed.thinkingLevel,
                    activeToolNames: [...this.seed.activeToolNames],
                };
                const state = {
                    tipId,
                    configuration: attachedConfiguration,
                    inbox: [],
                    lastOperationId: null,
                    operation: null,
                };
                const writes = [
                    ...(stored.kind === "absent" ? [setValue(branchTip(name), tipId)] : []),
                    setValue(laneConfig(name), attachedConfiguration),
                    setValue(laneState(name), {
                        currentOperationId: null,
                        lastOperationId: null,
                        inbox: [],
                    }),
                ];
                await mutator.commit(writes, context);
                lane = this.buildLane(name, state);
                this.lanesByName.set(name, lane);
                delivery = this.events.emitBatch([{ type: "lane_created", lane: name, at: tipId }], context);
            }, context);
        }
        catch (error) {
            if (this.closedError !== undefined)
                throw this.closedError;
            if (error instanceof InvalidLane || error instanceof UnknownTarget)
                throw error;
            throw this.fault(error, context);
        }
        await delivery;
        if (lane === undefined)
            throw this.fault(new SessionInvariantError(`Lane ${JSON.stringify(name)} was not published`), context);
        return lane;
    }
    async lanes(context) {
        this.assertOpen();
        const executions = await Promise.all([...this.lanesByName.values()].map((lane) => lane.inspectExecution(context)));
        return executions.map((execution) => ({
            name: execution.lane,
            tipId: execution.tipId,
            operation: execution.current,
        }));
    }
    getName(context) {
        this.assertOpen();
        return this.session.getName(context);
    }
    async setName(name, context) {
        this.assertOpen();
        let delivery;
        try {
            await this.session.mutate(async (mutator) => {
                this.assertOpen();
                await mutator.commit([name === undefined ? deleteValue(sessionName) : setValue(sessionName, name)], context);
                delivery = this.events.emitBatch([{ type: "value_update", value: "session_name", name }], context);
            }, context);
        }
        catch (error) {
            if (this.closedError !== undefined)
                throw this.closedError;
            throw this.fault(error, context);
        }
        await delivery;
    }
    getLabel(targetId, context) {
        this.assertOpen();
        return this.session.getLabel(targetId, context);
    }
    async setLabel(targetId, label, context) {
        this.assertOpen();
        let delivery;
        try {
            await this.session.mutate(async (mutator) => {
                this.assertOpen();
                const address = entryLabel(targetId);
                await mutator.commit([label === undefined ? deleteValue(address) : setValue(address, label)], context);
                delivery = this.events.emitBatch([{ type: "value_update", value: "entry_label", targetId, label }], context);
            }, context);
        }
        catch (error) {
            if (this.closedError !== undefined)
                throw this.closedError;
            throw this.fault(error, context);
        }
        await delivery;
    }
    getTools(context) {
        return this.getConfig("tools", context);
    }
    setTools(tools, context) {
        validateToolNames(tools);
        return this.setConfig("tools", tools, () => ({ type: "config_update", property: "tools" }), context);
    }
    getResources(context) {
        return this.getConfig("resources", context);
    }
    setResources(resources, context) {
        return this.setConfig("resources", resources, () => ({ type: "config_update", property: "resources" }), context);
    }
    getStreamOptions(context) {
        return this.getConfig("streamOptions", context);
    }
    setStreamOptions(options, context) {
        return this.setConfig("streamOptions", options, (previous, value) => ({ type: "config_update", property: "streamOptions", previous, value }), context);
    }
    getRetryPolicy(context) {
        return this.getConfig("retryPolicy", context);
    }
    setRetryPolicy(policy, context) {
        validateRetryPolicy(policy);
        return this.setConfig("retryPolicy", policy, (previous, value) => ({ type: "config_update", property: "retryPolicy", previous, value }), context);
    }
    getCompactionSettings(context) {
        return this.getConfig("compaction", context);
    }
    setCompactionSettings(compaction, context) {
        validateCompactionSettings(compaction);
        return this.setConfig("compaction", compaction, (previous, value) => ({ type: "config_update", property: "compactionSettings", previous, value }), context);
    }
    getSteeringMode(context) {
        return this.getConfig("steeringMode", context);
    }
    setSteeringMode(steeringMode, context) {
        return this.setConfig("steeringMode", steeringMode, (previous, value) => ({ type: "config_update", property: "steeringMode", previous, value }), context);
    }
    getFollowUpMode(context) {
        return this.getConfig("followUpMode", context);
    }
    setFollowUpMode(followUpMode, context) {
        return this.setConfig("followUpMode", followUpMode, (previous, value) => ({ type: "config_update", property: "followUpMode", previous, value }), context);
    }
    async watchSession(_context) {
        throw new SliceNotImplemented("watchSession");
    }
    fault(cause, context) {
        if (this.faultError !== undefined)
            return this.faultError;
        if (this.closedError !== undefined)
            return this.closedError;
        const normalized = cause instanceof Error ? cause : new Error(String(cause));
        const fault = new HarnessFault("AgentHarness storage or invariant fault", normalized);
        this.faultError = fault;
        for (const lane of this.lanesByName.values())
            lane.seal(fault);
        this.hooks.close(fault);
        void this.events.emit({ type: "fault", code: "harness_fault", message: fault.message }, context);
        this.events.close(fault);
        return fault;
    }
    close(context) {
        if (this.closePromise !== undefined)
            return this.closePromise;
        const error = new HarnessClosed();
        this.closedError = error;
        const idleCallbacks = [...this.lanesByName.values()].map((lane) => lane.seal(error));
        this.hooks.close(error);
        this.events.close(error);
        const sessionClose = this.session.close(context);
        this.closePromise = Promise.all([sessionClose, ...idleCallbacks]).then(() => undefined);
        return this.closePromise;
    }
    buildLane(name, state) {
        return new Lane(name, this.session, this.models, this.hooks, state, (cause, context) => this.fault(cause, context), (events, context) => this.events.emitBatch(events, context), (snapshot, filter, context, resnapshot) => this.events.watch(snapshot, filter, context, resnapshot), () => this.configStore.value);
    }
    async getConfig(key, _context) {
        this.assertOpen();
        return this.configStore.value[key];
    }
    async setConfig(key, value, event, context) {
        this.assertOpen();
        const previous = this.configStore.value[key];
        this.configStore.value = { ...this.configStore.value, [key]: value };
        await this.events.emit(event(previous, value), context);
    }
    assertOpen() {
        if (this.faultError !== undefined)
            throw this.faultError;
        if (this.closedError !== undefined)
            throw this.closedError;
    }
}
/** Attach runtime without starting provider, tool, hook, or timer effects. */
export async function createAgentHarness(options, context) {
    const tools = options.tools ?? [];
    validateToolNames(tools);
    validateRetryPolicy(options.retry ?? DEFAULT_RETRY_POLICY);
    validateCompactionSettings(options.compaction ?? DEFAULT_COMPACTION_SETTINGS);
    const seed = {
        model: { provider: options.model.provider, modelId: options.model.id },
        thinkingLevel: options.thinkingLevel ?? "off",
        activeToolNames: [...(options.activeToolNames ?? tools.map((tool) => tool.name))],
    };
    try {
        const restored = await restoreSession(options.session, context);
        const open = [...restored].flatMap(([lane, state]) => {
            const operation = state.operation;
            return operation === null
                ? []
                : [
                    {
                        lane,
                        operationId: operation.meta.operationId,
                        kind: operation.meta.intent.kind,
                        startedAt: operation.meta.startedAt,
                        ...(operation.state.control.status === "cancel_requested" ? { aborting: true } : {}),
                    },
                ];
        });
        return { harness: new Harness(options, seed, restored), open };
    }
    catch (error) {
        throw new HarnessFault("AgentHarness storage or invariant fault", error);
    }
}
//# sourceMappingURL=harness.js.map