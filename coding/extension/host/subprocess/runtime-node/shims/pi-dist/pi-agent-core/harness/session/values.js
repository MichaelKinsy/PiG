function validateAddress(namespace, key) {
    if (namespace.length === 0)
        throw new TypeError("Value namespace must not be empty");
    if (namespace.includes("\u0000"))
        throw new TypeError("Value namespace must not contain \\u0000");
    if (key.includes("\u0000"))
        throw new TypeError("Value key must not contain \\u0000");
}
export function value(namespace, key = "") {
    validateAddress(namespace, key);
    return Object.freeze({ namespace, key, kind: "value" });
}
export function list(namespace, key = "") {
    validateAddress(namespace, key);
    return Object.freeze({ namespace, key, kind: "list" });
}
export function setValue(address, next) {
    return {
        kind: "value",
        op: "set",
        namespace: address.namespace,
        key: address.key,
        value: next,
    };
}
export function deleteValue(address) {
    return {
        kind: "value",
        op: "delete",
        namespace: address.namespace,
        key: address.key,
    };
}
export function appendList(address, element) {
    return {
        kind: "list",
        op: "append",
        namespace: address.namespace,
        key: address.key,
        value: element,
    };
}
export function deleteList(address) {
    return {
        kind: "list",
        op: "delete",
        namespace: address.namespace,
        key: address.key,
    };
}
export function resolveListReadOptions(options = {}) {
    const requestedLimit = options.limit ?? 1_000;
    if (!Number.isSafeInteger(requestedLimit) || requestedLimit <= 0) {
        throw new TypeError("List read limit must be a positive safe integer");
    }
    return {
        ...(options.cursor === undefined ? {} : { cursor: options.cursor }),
        order: options.order ?? "asc",
        limit: Math.min(requestedLimit, 10_000),
    };
}
export const branchTip = (branch) => value("pi.branch.tip", branch);
export const branchTipInventoryPrefix = () => value("pi.branch.tip");
export const laneConfig = (lane) => value("pi.lane.config", lane);
export const laneState = (lane) => value("pi.lane.state", lane);
export const operationResult = (operationId) => value("pi.result", operationId);
export const operationMeta = (operationId) => value("pi.op.meta", operationId);
export const operationState = (operationId) => value("pi.op.state", operationId);
export const operationToolArgs = (operationId, stepId, sourceIndex) => value("pi.op.tool_args", `${operationId}:${stepId}:${sourceIndex}`);
export const operationToolMemo = (operationId, invocationId, name) => value("pi.op.tool_memo", `${operationId}:${invocationId}:${name}`);
export const operationPreparation = (operationId, taskId) => value("pi.op.preparation", `${operationId}:${taskId}`);
export const operationToolArgsPrefix = (operationId, stepId) => value("pi.op.tool_args", stepId === undefined ? `${operationId}:` : `${operationId}:${stepId}:`);
export const operationToolMemoPrefix = (operationId, invocationId) => value("pi.op.tool_memo", invocationId === undefined ? `${operationId}:` : `${operationId}:${invocationId}:`);
export const operationPreparationPrefix = (operationId) => value("pi.op.preparation", `${operationId}:`);
export const pendingEntry = (entryId) => value("pi.pending.entry", entryId);
export const pendingToolOutput = (operationId, invocationId) => value("pi.pending.tool_output", `${operationId}:${invocationId}`);
export const pendingAssistantFrames = (operationId, responseEntryId) => list("pi.pending.assistant_frame", `${operationId}:${responseEntryId}`);
export const pendingToolOutputPrefix = (operationId) => value("pi.pending.tool_output", `${operationId}:`);
export const sessionName = value("pi.session.name");
export const entryLabel = (entryId) => value("pi.entry.label", entryId);
//# sourceMappingURL=values.js.map