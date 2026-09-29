import { SessionInvariantError } from "../session/session.js";
import { branchTip, branchTipInventoryPrefix, laneConfig, laneState as laneStateValue, operationMeta, operationState, } from "../session/values.js";
function isSummaryState(state) {
    return state.at.startsWith("summary.");
}
function stateMatchesIntent(intent, state) {
    if (intent.kind === "compaction")
        return isSummaryState(state) && state.task.boundary.kind === "finish";
    if (intent.kind === "navigation") {
        if (state.at === "navigation.ready_to_commit") {
            return !intent.summarize && state.targetId === intent.targetId && state.label === intent.label;
        }
        return (intent.summarize &&
            isSummaryState(state) &&
            state.task.boundary.kind === "commit_navigation" &&
            state.task.boundary.targetId === intent.targetId &&
            state.task.boundary.label === intent.label &&
            state.task.customInstructions === intent.customInstructions);
    }
    return (state.at !== "navigation.ready_to_commit" &&
        (!isSummaryState(state) || state.task.boundary.kind === "resume_checkpoint"));
}
function classifyLaneStorage(lane, values) {
    const { tip, configuration, laneState } = values;
    if (tip === undefined && configuration === undefined && laneState === undefined) {
        return { kind: "absent" };
    }
    if (tip !== undefined && configuration === undefined && laneState === undefined) {
        return { kind: "branch", tip };
    }
    if (tip === undefined)
        throw new SessionInvariantError(`Lane ${JSON.stringify(lane)} is missing branch.tip`);
    if (configuration === undefined)
        throw new SessionInvariantError(`Lane ${JSON.stringify(lane)} is missing lane.config`);
    if (laneState === undefined)
        throw new SessionInvariantError(`Lane ${JSON.stringify(lane)} is missing lane.state`);
    return { kind: "lane", tip, configuration, laneState };
}
export async function readLaneStorage(reader, lane, context) {
    const [tip, configuration, laneState] = await Promise.all([
        reader.getValue(branchTip(lane), context),
        reader.getValue(laneConfig(lane), context),
        reader.getValue(laneStateValue(lane), context),
    ]);
    return classifyLaneStorage(lane, { tip, configuration, laneState });
}
/** Restore every complete configured AgentLane in one coherent Session read. */
export function restoreSession(session, context) {
    return session.mutate(async (reader) => {
        const [tips, configurations, states] = await Promise.all([
            reader.scanValues(branchTipInventoryPrefix(), context),
            reader.scanValues(laneConfig(""), context),
            reader.scanValues(laneStateValue(""), context),
        ]);
        const tipByLane = new Map(tips.map((value) => [value.address.key, value]));
        const configurationByLane = new Map(configurations.map((value) => [value.address.key, value]));
        const stateByLane = new Map(states.map((value) => [value.address.key, value]));
        const names = new Set([...tipByLane.keys(), ...configurationByLane.keys(), ...stateByLane.keys()]);
        const restored = new Map();
        for (const lane of names) {
            const stored = classifyLaneStorage(lane, {
                tip: tipByLane.get(lane),
                configuration: configurationByLane.get(lane),
                laneState: stateByLane.get(lane),
            });
            if (stored.kind !== "lane")
                continue;
            restored.set(lane, await restoreLaneState(reader, lane, stored, context));
        }
        return restored;
    }, context);
}
/** Restore one configured lane without starting work or interpreting its state. */
export function restoreLane(session, lane, context) {
    return session.mutate(async (reader) => {
        const stored = await readLaneStorage(reader, lane, context);
        if (stored.kind === "absent") {
            throw new SessionInvariantError(`Lane ${JSON.stringify(lane)} is missing branch.tip`);
        }
        if (stored.kind === "branch") {
            throw new SessionInvariantError(`Lane ${JSON.stringify(lane)} is missing lane.config`);
        }
        return restoreLaneState(reader, lane, stored, context);
    }, context);
}
export async function restoreLaneState(reader, lane, stored, context) {
    const operationId = stored.laneState.value.currentOperationId;
    let operation = null;
    if (operationId !== null) {
        const [meta, state] = await Promise.all([
            reader.getValue(operationMeta(operationId), context),
            reader.getValue(operationState(operationId), context),
        ]);
        if (meta === undefined)
            throw new SessionInvariantError(`Operation ${operationId} is missing op.meta`);
        if (state === undefined)
            throw new SessionInvariantError(`Operation ${operationId} is missing op.state`);
        if (meta.value.operationId !== operationId) {
            throw new SessionInvariantError(`Operation ${operationId} metadata names operation ${JSON.stringify(meta.value.operationId)}`);
        }
        if (meta.value.lane !== lane) {
            throw new SessionInvariantError(`Operation ${operationId} belongs to lane ${JSON.stringify(meta.value.lane)}, not ${JSON.stringify(lane)}`);
        }
        if (!stateMatchesIntent(meta.value.intent, state.value)) {
            throw new SessionInvariantError(`Operation ${operationId} intent ${meta.value.intent.kind} does not match state ${state.value.at}`);
        }
        operation = { meta: meta.value, state: state.value };
    }
    return {
        tipId: stored.tip.value,
        configuration: stored.configuration.value,
        inbox: stored.laneState.value.inbox,
        lastOperationId: stored.laneState.value.lastOperationId,
        operation,
    };
}
//# sourceMappingURL=restore.js.map