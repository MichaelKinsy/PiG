import { isJsonRpcId, isJsonRpcNotification, isJsonRpcRequest, isJsonRpcResponse, isObject, JSON_RPC_ERROR_CODES, McpAbortError, McpConnectionClosedError, McpError, McpTimeoutError, toError, } from "./protocol/jsonrpc.js";
import { LATEST_PROTOCOL_VERSION, SUPPORTED_PROTOCOL_VERSIONS, } from "./protocol/types.js";
const DEFAULT_REQUEST_TIMEOUT_MS = 30_000;
const MAX_LIST_PAGES = 1_000;
function validateInitializeResult(value) {
    if (!isObject(value) ||
        typeof value.protocolVersion !== "string" ||
        !isObject(value.capabilities) ||
        !isObject(value.serverInfo) ||
        typeof value.serverInfo.name !== "string" ||
        typeof value.serverInfo.version !== "string" ||
        (value.instructions !== undefined && typeof value.instructions !== "string")) {
        throw new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Invalid MCP initialize result");
    }
    return value;
}
function invalid(message) {
    return new McpError(JSON_RPC_ERROR_CODES.invalidRequest, message);
}
/** One page of a paginated list: the items under `key`, each checked by `isItem`. */
function validateListPage(method, key, value, isItem) {
    const items = isObject(value) ? value[key] : undefined;
    if (!isObject(value) || !Array.isArray(items))
        throw invalid(`Invalid MCP ${method} result`);
    for (const item of items) {
        if (!isObject(item) || !isItem(item))
            throw invalid(`Invalid entry in MCP ${method} result`);
    }
    // Some servers end pagination with `null` or `""` instead of omitting the cursor.
    const nextCursor = value.nextCursor === null || value.nextCursor === "" ? undefined : value.nextCursor;
    if (nextCursor !== undefined && typeof nextCursor !== "string")
        throw invalid(`Invalid MCP ${method} cursor`);
    return { items, ...(nextCursor === undefined ? {} : { nextCursor }) };
}
const isTool = (tool) => typeof tool.name === "string" && isObject(tool.inputSchema);
// `name` is required by the spec, but some servers omit it; the URI stands in.
const isResource = (resource) => typeof resource.uri === "string" && (resource.name === undefined || typeof resource.name === "string");
const isResourceTemplate = (template) => typeof template.uriTemplate === "string" && (template.name === undefined || typeof template.name === "string");
function toResource(item) {
    return { ...item, name: item.name ?? item.uri };
}
function toResourceTemplate(item) {
    return { ...item, name: item.name ?? item.uriTemplate };
}
function pageCursor(page) {
    return page.nextCursor === undefined ? {} : { nextCursor: page.nextCursor };
}
function validateReadResourceResult(value) {
    if (!isObject(value) || !Array.isArray(value.contents))
        throw invalid("Invalid MCP resources/read result");
    for (const contents of value.contents) {
        if (!isObject(contents) ||
            typeof contents.uri !== "string" ||
            (typeof contents.text !== "string" && typeof contents.blob !== "string")) {
            throw invalid("Invalid contents in MCP resources/read result");
        }
    }
    return value;
}
/** `content` is required by the spec, but servers that only return `structuredContent` omit it (the SDK defaults it too). */
function validateCallToolResult(value) {
    if (!isObject(value) || (value.content !== undefined && !Array.isArray(value.content))) {
        throw new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Invalid MCP tools/call result");
    }
    if (value.structuredContent !== undefined && !isObject(value.structuredContent)) {
        throw new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Invalid MCP tools/call structured content");
    }
    return (value.content === undefined ? { ...value, content: [] } : value);
}
export class McpClient {
    options;
    state = "idle";
    transport;
    nextRequestId = 1;
    serverInfoValue;
    serverCapabilitiesValue;
    instructionsValue;
    protocolVersionValue;
    pending = new Map();
    progressRequests = new Map();
    incoming = new Map();
    requestHandlers = new Map();
    notificationListeners = new Map();
    errorListeners = new Set();
    closeListeners = new Set();
    disposers = [];
    constructor(options) {
        this.options = Object.freeze({ ...options });
        this.requestHandlers.set("ping", () => ({}));
        const roots = options.roots;
        if (roots) {
            this.requestHandlers.set("roots/list", async () => ({
                roots: [...(typeof roots === "function" ? await roots() : roots)],
            }));
        }
    }
    get connectionState() {
        return this.state;
    }
    get serverInfo() {
        return this.serverInfoValue;
    }
    get serverCapabilities() {
        return this.serverCapabilitiesValue;
    }
    get instructions() {
        return this.instructionsValue;
    }
    get protocolVersion() {
        return this.protocolVersionValue;
    }
    async connect(transport) {
        if (this.state !== "idle")
            throw new Error(`Cannot connect MCP client in ${this.state} state`);
        this.state = "connecting";
        this.transport = transport;
        this.disposers = [
            transport.onMessage((message) => this.handleMessage(message)),
            // Transport errors are reported only. Pending requests fail when the transport closes.
            transport.onError((error) => this.emitError(error)),
            transport.onClose(() => this.handleTransportClose()),
        ];
        try {
            await transport.start();
            const capabilities = { ...this.options.capabilities };
            if (this.options.roots && capabilities.roots === undefined)
                capabilities.roots = {};
            const result = validateInitializeResult(await this.requestInternal("initialize", {
                protocolVersion: this.options.protocolVersion ?? LATEST_PROTOCOL_VERSION,
                capabilities,
                clientInfo: {
                    name: this.options.name,
                    version: this.options.version,
                    ...(this.options.title === undefined ? {} : { title: this.options.title }),
                },
            }, {}, true));
            if (!SUPPORTED_PROTOCOL_VERSIONS.includes(result.protocolVersion)) {
                throw new Error(`MCP server selected unsupported protocol version ${result.protocolVersion}`);
            }
            this.protocolVersionValue = result.protocolVersion;
            this.serverInfoValue = result.serverInfo;
            this.serverCapabilitiesValue = result.capabilities;
            this.instructionsValue = result.instructions;
            transport.setProtocolVersion?.(result.protocolVersion);
            await this.notifyInternal("notifications/initialized", undefined, true);
            this.state = "connected";
            return result;
        }
        catch (error) {
            await this.close().catch(() => { });
            throw error;
        }
    }
    request(method, params, options = {}) {
        return this.requestInternal(method, params, options, false);
    }
    notify(method, params) {
        return this.notifyInternal(method, params, false);
    }
    setRequestHandler(method, handler) {
        this.requestHandlers.set(method, handler);
        return () => {
            if (this.requestHandlers.get(method) === handler)
                this.requestHandlers.delete(method);
        };
    }
    onNotification(method, listener) {
        const listeners = this.notificationListeners.get(method) ?? new Set();
        this.notificationListeners.set(method, listeners);
        listeners.add(listener);
        return () => {
            listeners.delete(listener);
            if (listeners.size === 0)
                this.notificationListeners.delete(method);
        };
    }
    onError(listener) {
        this.errorListeners.add(listener);
        return () => this.errorListeners.delete(listener);
    }
    /** Called once when the connection closes, whether the transport dropped or `close()` was called. */
    onClose(listener) {
        this.closeListeners.add(listener);
        return () => this.closeListeners.delete(listener);
    }
    async ping(options = {}) {
        await this.request("ping", undefined, options);
    }
    async listTools(options = {}) {
        return (await this.listAll("tools/list", "tools", isTool, options));
    }
    /** Every resource, following `nextCursor` through all pages. */
    async listResources(options = {}) {
        return (await this.listAll("resources/list", "resources", isResource, options)).map(toResource);
    }
    /** One page of resources, starting at `cursor`. */
    async listResourcesPage(cursor, options = {}) {
        const page = await this.listPage("resources/list", "resources", isResource, cursor, options);
        return { resources: page.items.map(toResource), ...pageCursor(page) };
    }
    /** Every resource template, following `nextCursor` through all pages. */
    async listResourceTemplates(options = {}) {
        const templates = await this.listAll("resources/templates/list", "resourceTemplates", isResourceTemplate, options);
        return templates.map(toResourceTemplate);
    }
    /** One page of resource templates, starting at `cursor`. */
    async listResourceTemplatesPage(cursor, options = {}) {
        const page = await this.listPage("resources/templates/list", "resourceTemplates", isResourceTemplate, cursor, options);
        return { resourceTemplates: page.items.map(toResourceTemplate), ...pageCursor(page) };
    }
    async readResource(uri, options = {}) {
        return validateReadResourceResult(await this.request("resources/read", { uri }, options));
    }
    async listPage(method, key, isItem, cursor, options) {
        return validateListPage(method, key, await this.request(method, cursor === undefined ? undefined : { cursor }, options), isItem);
    }
    /** Every item of a paginated list method. */
    async listAll(method, key, isItem, options) {
        const items = [];
        const cursors = new Set();
        let cursor;
        for (let pageNumber = 0; pageNumber < MAX_LIST_PAGES; pageNumber++) {
            const page = await this.listPage(method, key, isItem, cursor, options);
            items.push(...page.items);
            if (page.nextCursor === undefined)
                return items;
            if (cursors.has(page.nextCursor))
                throw new Error(`MCP ${method} returned duplicate cursor: ${page.nextCursor}`);
            cursors.add(page.nextCursor);
            cursor = page.nextCursor;
        }
        throw new Error(`MCP ${method} exceeded ${MAX_LIST_PAGES} pages`);
    }
    async callTool(name, args, options = {}) {
        return validateCallToolResult(await this.request("tools/call", { name, ...(args === undefined ? {} : { arguments: args }) }, options));
    }
    async close() {
        const transport = this.transport;
        this.transport = undefined;
        this.disposeTransportListeners();
        this.markClosed(new McpConnectionClosedError());
        await transport?.close();
    }
    async requestInternal(method, params, options, allowConnecting) {
        const transport = this.requireTransport(allowConnecting);
        if (options.signal?.aborted)
            throw new McpAbortError();
        const id = this.nextRequestId++;
        const progressToken = options.onProgress ? id : undefined;
        const requestParams = progressToken === undefined
            ? params
            : { ...params, _meta: { ...(isObject(params?._meta) ? params._meta : {}), progressToken } };
        const message = {
            jsonrpc: "2.0",
            id,
            method,
            ...(requestParams === undefined ? {} : { params: requestParams }),
        };
        return new Promise((resolve, reject) => {
            const entry = {
                resolve,
                reject,
                timeoutMs: options.timeoutMs ?? this.options.requestTimeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS,
                timer: undefined,
                signal: options.signal,
                // The spec forbids cancelling `initialize`.
                onAbort: () => this.cancelPending(id, new McpAbortError(), method !== "initialize", String(options.signal?.reason ?? "Aborted")),
                cancellable: method !== "initialize",
                onProgress: options.onProgress,
                progressToken,
            };
            this.pending.set(id, entry);
            if (progressToken !== undefined)
                this.progressRequests.set(progressToken, id);
            options.signal?.addEventListener("abort", entry.onAbort, { once: true });
            this.armTimeout(id, entry);
            transport.send(message).catch((error) => this.cancelPending(id, error, false));
        });
    }
    async notifyInternal(method, params, allowConnecting) {
        await this.requireTransport(allowConnecting).send({
            jsonrpc: "2.0",
            method,
            ...(params === undefined ? {} : { params }),
        });
    }
    requireTransport(allowConnecting) {
        if (this.transport && (this.state === "connected" || (allowConnecting && this.state === "connecting"))) {
            return this.transport;
        }
        throw new McpConnectionClosedError(`MCP client is ${this.state}`);
    }
    handleMessage(message) {
        if (isJsonRpcResponse(message)) {
            this.handleResponse(message);
            return;
        }
        if (isJsonRpcRequest(message)) {
            void this.handleRequest(message);
            return;
        }
        if (isJsonRpcNotification(message)) {
            this.handleNotification(message.method, message.params);
            return;
        }
        this.emitError(new McpError(JSON_RPC_ERROR_CODES.invalidRequest, "Received invalid JSON-RPC message"));
    }
    handleResponse(message) {
        const entry = this.pending.get(message.id);
        if (!entry) {
            this.emitError(new Error(`Received response for unknown MCP request ${String(message.id)}`));
            return;
        }
        this.removePending(message.id, entry);
        if ("error" in message)
            entry.reject(new McpError(message.error.code, message.error.message, message.error.data));
        else
            entry.resolve(message.result);
    }
    async handleRequest(message) {
        const transport = this.transport;
        if (!transport)
            return;
        const handler = this.requestHandlers.get(message.method);
        if (!handler) {
            await transport
                .send({
                jsonrpc: "2.0",
                id: message.id,
                error: { code: JSON_RPC_ERROR_CODES.methodNotFound, message: `Method not found: ${message.method}` },
            })
                .catch((error) => this.emitError(error));
            return;
        }
        const controller = new AbortController();
        this.incoming.set(message.id, controller);
        try {
            const result = await handler(message.params, { signal: controller.signal });
            await transport.send({ jsonrpc: "2.0", id: message.id, result: result ?? {} });
        }
        catch (error) {
            const responseError = error instanceof McpError
                ? { code: error.code, message: error.message, data: error.data }
                : { code: JSON_RPC_ERROR_CODES.internalError, message: toError(error).message };
            await transport
                .send({ jsonrpc: "2.0", id: message.id, error: responseError })
                .catch((sendError) => this.emitError(sendError));
        }
        finally {
            this.incoming.delete(message.id);
        }
    }
    handleNotification(method, params) {
        if (method === "notifications/progress")
            this.handleProgress(params);
        else if (method === "notifications/cancelled")
            this.handleCancelled(params);
        for (const listener of this.notificationListeners.get(method) ?? []) {
            try {
                listener(params);
            }
            catch (error) {
                this.emitError(error);
            }
        }
    }
    handleProgress(params) {
        if (!isObject(params) || !isJsonRpcId(params.progressToken) || typeof params.progress !== "number")
            return;
        const requestId = this.progressRequests.get(params.progressToken);
        const entry = requestId === undefined ? undefined : this.pending.get(requestId);
        if (requestId === undefined || !entry)
            return;
        this.armTimeout(requestId, entry);
        try {
            entry.onProgress?.(params);
        }
        catch (error) {
            this.emitError(error);
        }
    }
    handleCancelled(params) {
        if (isObject(params) && isJsonRpcId(params.requestId))
            this.incoming.get(params.requestId)?.abort(params.reason);
    }
    armTimeout(id, entry) {
        if (entry.timer)
            clearTimeout(entry.timer);
        if (!Number.isFinite(entry.timeoutMs) || entry.timeoutMs <= 0)
            return;
        entry.timer = setTimeout(() => {
            this.cancelPending(id, new McpTimeoutError(entry.timeoutMs), entry.cancellable, "Request timed out");
        }, entry.timeoutMs);
    }
    cancelPending(id, error, notifyServer, reason) {
        const entry = this.pending.get(id);
        if (!entry)
            return;
        this.removePending(id, entry);
        entry.reject(error);
        if (notifyServer && this.transport) {
            void this.transport
                .send({
                jsonrpc: "2.0",
                method: "notifications/cancelled",
                params: { requestId: id, ...(reason ? { reason } : {}) },
            })
                .catch((sendError) => this.emitError(sendError));
        }
    }
    removePending(id, entry) {
        this.pending.delete(id);
        if (entry.timer)
            clearTimeout(entry.timer);
        if (entry.progressToken !== undefined)
            this.progressRequests.delete(entry.progressToken);
        entry.signal?.removeEventListener("abort", entry.onAbort);
    }
    rejectPending(error) {
        for (const [id, entry] of this.pending) {
            this.removePending(id, entry);
            entry.reject(error);
        }
    }
    handleTransportClose() {
        this.markClosed(new McpConnectionClosedError());
    }
    /** Idempotent: rejects in-flight requests, aborts server requests we are serving, and flips the state. */
    markClosed(error) {
        const wasClosed = this.state === "closed";
        this.state = "closed";
        this.rejectPending(error);
        for (const controller of this.incoming.values())
            controller.abort(error);
        this.incoming.clear();
        if (wasClosed)
            return;
        for (const listener of [...this.closeListeners]) {
            try {
                listener();
            }
            catch (listenerError) {
                this.emitError(listenerError);
            }
        }
    }
    emitError(error) {
        const normalized = toError(error);
        for (const listener of this.errorListeners)
            listener(normalized);
    }
    disposeTransportListeners() {
        for (const dispose of this.disposers.splice(0))
            dispose();
    }
}
//# sourceMappingURL=client.js.map