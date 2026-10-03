export const JSON_RPC_ERROR_CODES = {
    parseError: -32700,
    invalidRequest: -32600,
    methodNotFound: -32601,
    invalidParams: -32602,
    internalError: -32603,
};
export class McpError extends Error {
    code;
    data;
    constructor(code, message, data) {
        super(message);
        this.name = "McpError";
        this.code = code;
        this.data = data;
    }
}
export class McpConnectionClosedError extends Error {
    constructor(message = "MCP connection closed") {
        super(message);
        this.name = "McpConnectionClosedError";
    }
}
export class McpTimeoutError extends Error {
    timeoutMs;
    constructor(timeoutMs) {
        super(`MCP request timed out after ${timeoutMs}ms`);
        this.name = "McpTimeoutError";
        this.timeoutMs = timeoutMs;
    }
}
export class McpAbortError extends Error {
    constructor(message = "MCP request aborted") {
        super(message);
        this.name = "AbortError";
    }
}
export function isObject(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
export function toError(value) {
    return value instanceof Error ? value : new Error(String(value));
}
export function isJsonRpcId(value) {
    return typeof value === "string" || (typeof value === "number" && Number.isFinite(value));
}
export function isJsonRpcRequest(message) {
    return (isObject(message) && message.jsonrpc === "2.0" && isJsonRpcId(message.id) && typeof message.method === "string");
}
export function isJsonRpcNotification(message) {
    return isObject(message) && message.jsonrpc === "2.0" && !("id" in message) && typeof message.method === "string";
}
export function isJsonRpcResponse(message) {
    if (!isObject(message) || message.jsonrpc !== "2.0" || !isJsonRpcId(message.id))
        return false;
    if ("result" in message)
        return !("error" in message);
    if (!("error" in message) || !isObject(message.error))
        return false;
    return typeof message.error.code === "number" && typeof message.error.message === "string";
}
export function parseJsonRpcMessage(value) {
    if (isJsonRpcRequest(value) || isJsonRpcNotification(value) || isJsonRpcResponse(value))
        return value;
    throw new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Invalid JSON-RPC message");
}
//# sourceMappingURL=jsonrpc.js.map