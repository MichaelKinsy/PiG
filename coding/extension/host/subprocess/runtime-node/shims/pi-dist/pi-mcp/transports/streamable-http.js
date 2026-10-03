import { isJsonRpcRequest, isJsonRpcResponse, JSON_RPC_ERROR_CODES, McpConnectionClosedError, parseJsonRpcMessage, toError, } from "../protocol/jsonrpc.js";
import { DEFAULT_MAX_MESSAGE_BYTES, TransportEvents } from "./transport.js";
const MAX_ERROR_BODY_BYTES = 8 * 1024;
const ERROR_MESSAGE_BODY_CHARS = 500;
const DEFAULT_RECONNECT_INITIAL_DELAY_MS = 1_000;
const DEFAULT_RECONNECT_MAX_DELAY_MS = 30_000;
const DEFAULT_RECONNECT_MAX_RETRIES = 5;
export async function consumeSseStream(stream, options) {
    const reader = stream.getReader();
    const decoder = new TextDecoder();
    let buffered = "";
    let eventName;
    let eventId;
    let dataLines = [];
    // Bytes of the pending event's data, including the "\n" joins, so events streamed as many
    // short `data:` lines without a terminating blank line cannot grow without bound.
    let dataBytes = 0;
    const maxEventBytes = options.maxEventBytes ?? DEFAULT_MAX_MESSAGE_BYTES;
    const dispatch = () => {
        if (dataLines.length === 0) {
            eventName = undefined;
            eventId = undefined;
            return;
        }
        const data = dataLines.join("\n");
        options.onEvent({ ...(eventName ? { event: eventName } : {}), data, ...(eventId ? { id: eventId } : {}) });
        eventName = undefined;
        eventId = undefined;
        dataLines = [];
        dataBytes = 0;
    };
    const processLine = (rawLine) => {
        const line = rawLine.endsWith("\r") ? rawLine.slice(0, -1) : rawLine;
        if (line === "") {
            dispatch();
            return;
        }
        if (line.startsWith(":"))
            return;
        const colon = line.indexOf(":");
        const field = colon < 0 ? line : line.slice(0, colon);
        let value = colon < 0 ? "" : line.slice(colon + 1);
        if (value.startsWith(" "))
            value = value.slice(1);
        if (field === "data") {
            dataBytes += Buffer.byteLength(value) + (dataLines.length > 0 ? 1 : 0);
            if (dataBytes > maxEventBytes)
                throw new Error(`MCP SSE event exceeds ${maxEventBytes} bytes`);
            dataLines.push(value);
        }
        else if (field === "event")
            eventName = value;
        else if (field === "id" && !value.includes("\0")) {
            eventId = value;
            options.onId?.(value);
        }
        else if (field === "retry" && /^\d+$/.test(value))
            options.onRetry?.(Number(value));
    };
    try {
        while (true) {
            const { done, value } = await reader.read();
            if (done)
                break;
            buffered += decoder.decode(value, { stream: true });
            let newline = buffered.indexOf("\n");
            while (newline >= 0) {
                processLine(buffered.slice(0, newline));
                buffered = buffered.slice(newline + 1);
                newline = buffered.indexOf("\n");
            }
            if (Buffer.byteLength(buffered) > maxEventBytes)
                throw new Error(`MCP SSE event exceeds ${maxEventBytes} bytes`);
        }
        buffered += decoder.decode();
        if (buffered)
            processLine(buffered);
        dispatch();
    }
    finally {
        reader.releaseLock();
    }
}
export class McpHttpError extends Error {
    status;
    body;
    constructor(status, message, body = "") {
        super(message);
        this.name = "McpHttpError";
        this.status = status;
        this.body = body;
    }
}
export class McpAuthRequiredError extends McpHttpError {
    wwwAuthenticate;
    constructor(response, body = "") {
        super(401, "MCP server requires authentication", body);
        this.name = "McpAuthRequiredError";
        this.wwwAuthenticate = response.headers.get("www-authenticate");
    }
}
export class McpSessionExpiredError extends McpHttpError {
    constructor(body = "") {
        super(404, "MCP session expired", body);
        this.name = "McpSessionExpiredError";
    }
}
function contentType(response) {
    return response.headers.get("content-type")?.split(";", 1)[0]?.trim().toLowerCase();
}
/** 401, or 403 with an `insufficient_scope` bearer challenge (step-up authorization). */
function needsAuthorization(response) {
    if (response.status === 401)
        return true;
    if (response.status !== 403)
        return false;
    return /(?:^|[\s,])error="?insufficient_scope"?/i.test(response.headers.get("www-authenticate") ?? "");
}
/** Statuses worth retrying when a stream fails to (re)open. */
function isTransientStatus(status) {
    return status === 408 || status === 429 || status >= 500;
}
function discard(response) {
    return response.body?.cancel().catch(() => { }) ?? Promise.resolve();
}
function describeHttpFailure(status, body) {
    const text = body.trim();
    const snippet = text.length > ERROR_MESSAGE_BODY_CHARS ? `${text.slice(0, ERROR_MESSAGE_BODY_CHARS - 3)}...` : text;
    return `MCP HTTP request failed with status ${status}${snippet ? `: ${snippet}` : ""}`;
}
export class StreamableHttpTransport extends TransportEvents {
    url;
    options;
    fetch;
    controller = new AbortController();
    started = false;
    closed = false;
    sessionIdValue;
    protocolVersion;
    getStreamStarted = false;
    constructor(options) {
        super();
        this.options = Object.freeze({ ...options, headers: options.headers ? { ...options.headers } : undefined });
        this.url = new URL(options.url);
        const fetch = options.fetch ?? globalThis.fetch;
        // Call fetch without a receiver. `this.fetch(...)` and `context.fetch(...)` would pass the transport or
        // the auth context as `this`, which Cloudflare Workers reject for the platform fetch ("Illegal invocation").
        this.fetch = (input, init) => fetch(input, init);
    }
    get sessionId() {
        return this.sessionIdValue;
    }
    async start() {
        if (this.started)
            throw new Error("MCP Streamable HTTP transport already started");
        if (this.closed)
            throw new McpConnectionClosedError();
        this.started = true;
    }
    setProtocolVersion(version) {
        this.protocolVersion = version;
    }
    async send(message) {
        if (!this.started || this.closed)
            throw new McpConnectionClosedError();
        const response = await this.authorizedFetch("POST", {
            headers: { accept: "application/json, text/event-stream", "content-type": "application/json" },
            body: JSON.stringify(message),
        });
        await this.checkResponse(response);
        this.captureSession(response);
        if (!isJsonRpcRequest(message)) {
            // Notifications and responses are acknowledged with 202 and carry no reply; ignore any body.
            await discard(response);
            // The server-to-client stream may only open once the session is initialized.
            if ("method" in message && message.method === "notifications/initialized")
                this.startGetStream();
            return;
        }
        if (response.status === 202 || response.status === 204) {
            throw new McpHttpError(response.status, `MCP server accepted request ${message.method} without a response`);
        }
        const type = contentType(response);
        if (type === "application/json") {
            const body = await response.json();
            for (const item of Array.isArray(body) ? body : [body])
                this.emitMessage(parseJsonRpcMessage(item));
            return;
        }
        if (type === "text/event-stream" && response.body) {
            void this.consumeResponseStream(response.body, message.id);
            return;
        }
        await discard(response);
        throw new McpHttpError(response.status, `Unsupported MCP response content type: ${type ?? "missing"}`);
    }
    async close() {
        if (this.closed)
            return;
        this.closed = true;
        this.controller.abort();
        if (this.started && this.sessionIdValue) {
            const controller = new AbortController();
            const timeout = setTimeout(() => controller.abort(), 1_000);
            try {
                const { headers } = await this.headers();
                await this.fetch(this.url, { method: "DELETE", headers, signal: controller.signal })
                    .then(discard)
                    .catch(() => undefined);
            }
            catch {
                // Resolving auth headers failed; the session will expire on the server.
            }
            finally {
                clearTimeout(timeout);
            }
        }
        this.emitClose();
    }
    /**
     * Fetch with auth headers. A 401 (or a 403 asking for more scope) is handed to the auth provider
     * once, and the request is retried with whatever credentials it left behind.
     */
    async authorizedFetch(method, init) {
        const onUnauthorized = this.options.authProvider?.onUnauthorized?.bind(this.options.authProvider);
        for (let attempt = 0;; attempt++) {
            const { headers, token } = await this.headers(init.headers);
            const response = await this.fetch(this.url, {
                method,
                headers,
                body: init.body,
                signal: this.controller.signal,
            });
            if (attempt > 0 || !onUnauthorized || !needsAuthorization(response))
                return response;
            try {
                await onUnauthorized({ response, serverUrl: this.url, fetch: this.fetch, token });
            }
            finally {
                await discard(response);
            }
        }
    }
    async headers(extra = {}) {
        const headers = new Headers(this.options.headers);
        for (const [name, value] of Object.entries(extra))
            headers.set(name, value);
        if (this.sessionIdValue)
            headers.set("Mcp-Session-Id", this.sessionIdValue);
        if (this.protocolVersion)
            headers.set("MCP-Protocol-Version", this.protocolVersion);
        const token = await this.options.authProvider?.token();
        if (token)
            headers.set("Authorization", `Bearer ${token}`);
        return { headers, ...(token ? { token } : {}) };
    }
    captureSession(response) {
        const sessionId = response.headers.get("mcp-session-id");
        if (sessionId)
            this.sessionIdValue = sessionId;
    }
    async checkResponse(response) {
        if (response.ok)
            return;
        const body = (await response.text().catch(() => "")).slice(0, MAX_ERROR_BODY_BYTES);
        if (response.status === 401)
            throw new McpAuthRequiredError(response, body);
        if (response.status === 404 && this.sessionIdValue)
            throw new McpSessionExpiredError(body);
        throw new McpHttpError(response.status, describeHttpFailure(response.status, body), body);
    }
    async consumeSse(stream, cursor, onMessage) {
        await consumeSseStream(stream, {
            maxEventBytes: this.options.maxMessageBytes ?? DEFAULT_MAX_MESSAGE_BYTES,
            onId: (id) => {
                cursor.lastEventId = id;
            },
            onRetry: (delayMs) => {
                cursor.retryMs = delayMs;
            },
            onEvent: (event) => {
                cursor.received = true;
                // Events without data prime resumption; other event types are not JSON-RPC.
                if (!event.data.trim() || (event.event !== undefined && event.event !== "message"))
                    return;
                let message;
                try {
                    message = parseJsonRpcMessage(JSON.parse(event.data));
                }
                catch (error) {
                    this.emitError(error);
                    return;
                }
                onMessage?.(message);
                this.emitMessage(message);
            },
        });
    }
    /**
     * Read the SSE stream answering one request. When the stream ends or breaks before the response
     * arrives and the server assigned event IDs, resume it with GET and `Last-Event-ID`, as the
     * server may close response streams at will. Otherwise only this request fails.
     */
    async consumeResponseStream(body, requestId) {
        const cursor = { lastEventId: undefined, retryMs: undefined, received: false };
        let answered = false;
        const onMessage = (message) => {
            if (isJsonRpcResponse(message) && message.id === requestId)
                answered = true;
        };
        let stream = body;
        let failure;
        for (let attempt = 0;;) {
            if (stream) {
                try {
                    await this.consumeSse(stream, cursor, onMessage);
                    failure = undefined;
                }
                catch (error) {
                    failure = error;
                }
            }
            if (answered || this.closed)
                return;
            if (failure !== undefined && !this.isRetryable(failure))
                break;
            if (cursor.lastEventId === undefined || attempt >= this.maxRetries())
                break;
            if (cursor.received)
                attempt = 0;
            cursor.received = false;
            if (!(await this.sleep(this.reconnectDelay(attempt++, cursor.retryMs))))
                return;
            try {
                stream = await this.openSseStream(cursor.lastEventId);
            }
            catch (error) {
                failure = error;
                if (!this.isRetryable(error))
                    break;
                stream = undefined;
            }
        }
        if (this.closed)
            return;
        const reason = failure === undefined ? "stream ended without a response" : toError(failure).message;
        this.emitMessage({
            jsonrpc: "2.0",
            id: requestId,
            error: { code: JSON_RPC_ERROR_CODES.internalError, message: `MCP response stream failed: ${reason}` },
        });
    }
    startGetStream() {
        if (this.options.openGetStream === false || this.getStreamStarted || this.closed)
            return;
        this.getStreamStarted = true;
        void this.runGetStream();
    }
    /** Keep the server-to-client stream open, reconnecting with backoff when it drops. */
    async runGetStream() {
        const cursor = { lastEventId: undefined, retryMs: undefined, received: false };
        for (let attempt = 0; !this.closed;) {
            try {
                const stream = await this.openSseStream(cursor.lastEventId);
                // The server does not offer a GET stream.
                if (!stream)
                    return;
                const openedAt = Date.now();
                await this.consumeSse(stream, cursor);
                // A stream that stayed up for a while counts as healthy, even if it was idle.
                if (cursor.received || Date.now() - openedAt > this.maxDelay())
                    attempt = 0;
            }
            catch (error) {
                if (this.closed)
                    return;
                if (!this.isRetryable(error)) {
                    this.emitError(error);
                    return;
                }
            }
            cursor.received = false;
            if (attempt >= this.maxRetries()) {
                this.emitError(new Error("MCP server-to-client stream dropped and could not be reopened"));
                return;
            }
            if (!(await this.sleep(this.reconnectDelay(attempt++, cursor.retryMs))))
                return;
        }
    }
    /** Open a GET SSE stream. Resolves to undefined when the server answers 405 (no GET stream). */
    async openSseStream(lastEventId) {
        const response = await this.authorizedFetch("GET", {
            headers: {
                accept: "text/event-stream",
                ...(lastEventId === undefined ? {} : { "last-event-id": lastEventId }),
            },
        });
        if (response.status === 405) {
            await discard(response);
            return undefined;
        }
        await this.checkResponse(response);
        this.captureSession(response);
        const type = contentType(response);
        if (type !== "text/event-stream" || !response.body) {
            await discard(response);
            throw new McpHttpError(response.status, `Unsupported MCP GET response content type: ${type ?? "missing"}`);
        }
        return response.body;
    }
    /**
     * Network failures and transient statuses are retried; auth, session, and protocol errors are not.
     * `fetch` reports network failures, including a connection dropped mid-body, as `TypeError`.
     */
    isRetryable(error) {
        if (error instanceof McpHttpError)
            return isTransientStatus(error.status);
        if (error instanceof TypeError)
            return true;
        const code = error?.code;
        return typeof code === "string" && (code.startsWith("E") || code.startsWith("UND_ERR"));
    }
    reconnectDelay(attempt, serverDelayMs) {
        if (serverDelayMs !== undefined)
            return serverDelayMs;
        const initial = this.options.reconnect?.initialDelayMs ?? DEFAULT_RECONNECT_INITIAL_DELAY_MS;
        return Math.min(initial * 2 ** attempt, this.maxDelay());
    }
    maxDelay() {
        return this.options.reconnect?.maxDelayMs ?? DEFAULT_RECONNECT_MAX_DELAY_MS;
    }
    maxRetries() {
        return this.options.reconnect?.maxRetries ?? DEFAULT_RECONNECT_MAX_RETRIES;
    }
    /** Resolves false when the transport closed while waiting. */
    sleep(ms) {
        const signal = this.controller.signal;
        if (signal.aborted)
            return Promise.resolve(false);
        return new Promise((resolve) => {
            const onAbort = () => {
                clearTimeout(timer);
                resolve(false);
            };
            const timer = setTimeout(() => {
                signal.removeEventListener("abort", onAbort);
                resolve(true);
            }, ms);
            // A reconnect wait alone does not keep the process alive.
            timer.unref?.();
            signal.addEventListener("abort", onAbort, { once: true });
        });
    }
}
//# sourceMappingURL=streamable-http.js.map