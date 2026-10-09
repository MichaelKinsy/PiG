import { Worker } from "node:worker_threads";
import { toCodemodeIdentifier } from "../identifier.js";
import { loadQuickJSWasm } from "../wasm.js";
import { isWorkerToHostMessage, } from "./protocol.js";
const DEFAULT_TIMEOUT_MS = 300_000;
const IDENTIFIER = /^[A-Za-z_$][A-Za-z0-9_$]*$/;
const RESERVED_GLOBALS = new Set([
    "tools",
    "ALL_TOOLS",
    "console",
    "text",
    "image",
    "exit",
    "globalThis",
    "store",
    "load",
]);
function errorMessage(error) {
    return error instanceof Error ? error.message : String(error);
}
function serializeStore(store) {
    const serialized = {};
    for (const [key, value] of Object.entries(store ?? {})) {
        const json = JSON.stringify(value);
        if (json !== undefined)
            serialized[key] = json;
    }
    return serialized;
}
/**
 * The prelude that serializes values for the host runs in the same VM as the script, so a script
 * that patches built-ins (for example `Array.prototype.toJSON`) can make it send malformed data.
 * Payloads that do not decode throw this error, and the execution fails as a sandbox error.
 */
class BridgeError extends Error {
}
function parseBridgeJson(json, what) {
    try {
        return JSON.parse(json);
    }
    catch {
        throw new BridgeError(`${what} is not valid JSON`);
    }
}
function parseStoreWrites(json) {
    const entries = parseBridgeJson(json, "store writes");
    if (!Array.isArray(entries))
        throw new BridgeError("store writes are not an array");
    const writes = { set: {}, delete: [] };
    for (const entry of entries) {
        if (!Array.isArray(entry) ||
            typeof entry[0] !== "string" ||
            (entry.length === 2 ? typeof entry[1] !== "string" : entry.length !== 1)) {
            throw new BridgeError("store writes contain a malformed entry");
        }
        const [key, value] = entry;
        if (value === undefined)
            writes.delete.push(key);
        else
            writes.set[key] = parseBridgeJson(value, `store value for ${JSON.stringify(key)}`);
    }
    return writes;
}
function parseScriptError(json) {
    const parsed = parseBridgeJson(json, "script error");
    if (typeof parsed !== "object" || parsed === null)
        throw new BridgeError("script error is not an object");
    const { name, message, stack } = parsed;
    if (typeof message !== "string" ||
        (name !== undefined && typeof name !== "string") ||
        (stack !== undefined && typeof stack !== "string")) {
        throw new BridgeError("script error is malformed");
    }
    return { kind: "script", name, message, stack };
}
function defaultWorkerUrl() {
    // `.ts` when running from source (tests, tsx), `.js` from the published dist.
    return new URL(import.meta.url.endsWith(".ts") ? "./worker.ts" : "./worker.js", import.meta.url);
}
/**
 * One script run in its own worker and QuickJS VM. A fresh worker per run keeps
 * termination simple: a runaway script, including one that only spins the
 * microtask queue, is killed with `terminate()` and cannot poison a later run.
 */
class Execution {
    promise;
    resolveResult;
    worker;
    interrupt = new SharedArrayBuffer(4);
    tools;
    globals;
    signal;
    timer;
    output = [];
    calls = [];
    pending = new Map();
    finished = false;
    constructor(options) {
        this.promise = new Promise((resolve) => {
            this.resolveResult = resolve;
        });
        this.tools = options.tools;
        this.globals = options.globals;
        this.signal = options.signal;
        if (Number.isFinite(options.timeoutMs)) {
            this.timer = setTimeout(() => {
                this.finish({ kind: "timeout", message: `Execution timed out after ${options.timeoutMs} ms` });
            }, options.timeoutMs);
        }
        if (options.signal) {
            if (options.signal.aborted) {
                this.onAbort();
            }
            else {
                options.signal.addEventListener("abort", this.onAbort, { once: true });
            }
        }
        options.wasm.then((wasm) => this.start(options, wasm), (error) => {
            this.finish({ kind: "sandbox", message: `Failed to load QuickJS: ${errorMessage(error)}` });
        });
    }
    abort(message) {
        this.finish({ kind: "aborted", message });
        return this.promise;
    }
    start(options, wasm) {
        if (this.finished)
            return;
        const workerData = {
            code: options.code,
            tools: [...options.tools.values()].map((tool) => ({
                name: tool.name,
                jsName: toCodemodeIdentifier(tool.name),
                description: tool.description ?? "",
            })),
            globals: [...options.globals.values()].map((global) => ({
                name: global.name,
                spread: global.spread === true,
            })),
            wasm,
            memoryLimitBytes: options.memoryLimitBytes,
            store: options.store,
            interrupt: this.interrupt,
        };
        let worker;
        try {
            worker = new Worker(options.workerUrl, { workerData });
        }
        catch (error) {
            this.finish({ kind: "sandbox", message: `Failed to start worker: ${errorMessage(error)}` });
            return;
        }
        this.worker = worker;
        worker.on("message", (message) => {
            // An exception here would be uncaught in the host process and leave the execution unsettled.
            try {
                this.handleMessage(message);
            }
            catch (error) {
                this.finish({
                    kind: "sandbox",
                    message: error instanceof BridgeError
                        ? `Sandbox bridge broken: ${error.message}. The script may have modified built-ins such as a prototype's toJSON.`
                        : `Sandbox host failed: ${errorMessage(error)}`,
                });
            }
        });
        worker.on("error", (error) => {
            this.finish({
                kind: "sandbox",
                name: error instanceof Error ? error.name : undefined,
                message: errorMessage(error),
            });
        });
        worker.on("exit", (code) => {
            this.finish({ kind: "sandbox", message: `Worker exited with code ${code} before the script settled` });
        });
    }
    onAbort = () => {
        const reason = this.signal?.reason;
        this.finish({ kind: "aborted", message: reason instanceof Error ? reason.message : "Execution aborted" });
    };
    post(message) {
        this.worker?.postMessage(message);
    }
    handleMessage(message) {
        if (this.finished)
            return;
        if (!isWorkerToHostMessage(message))
            throw new BridgeError("unknown message from the worker");
        switch (message.type) {
            case "output":
                this.output.push(message.item);
                break;
            case "call":
                if (this.pending.has(message.id))
                    throw new BridgeError(`duplicate call id ${message.id}`);
                void this.handleCall(message);
                break;
            case "done":
                this.handleDone(message);
                break;
            case "crash":
                this.finish({ kind: "sandbox", message: message.message });
                break;
        }
    }
    handleDone(message) {
        // Decode everything before finish(), which must not throw once it starts.
        if (!message.ok) {
            this.finish(parseScriptError(message.error));
            return;
        }
        const value = message.value === undefined ? undefined : parseBridgeJson(message.value, "return value");
        this.finish(undefined, value, parseStoreWrites(message.writes));
    }
    async handleCall(message) {
        const { id, name } = message;
        const isTool = message.target === "tool";
        const record = isTool ? { name, status: "cancelled", durationMs: 0 } : undefined;
        if (record)
            this.calls.push(record);
        const pending = { record, startedAt: performance.now(), controller: new AbortController() };
        this.pending.set(id, pending);
        let status;
        let reply;
        try {
            const tool = (isTool ? this.tools : this.globals).get(name);
            if (!tool)
                throw new Error(`Unknown ${isTool ? "tool" : "global"} "${name}"`);
            const args = message.args === undefined ? undefined : JSON.parse(message.args);
            const value = await tool.execute(args, { signal: pending.controller.signal });
            reply = { type: "result", id, ok: true, payload: value === undefined ? undefined : JSON.stringify(value) };
            status = "ok";
        }
        catch (error) {
            reply = { type: "result", id, ok: false, payload: errorMessage(error) };
            status = "error";
        }
        // Already cancelled by finish(): the record keeps "cancelled" and the
        // worker is gone or going.
        if (!this.pending.delete(id))
            return;
        if (record) {
            record.status = status;
            record.durationMs = performance.now() - pending.startedAt;
        }
        this.post(reply);
    }
    finish(error, value, writes) {
        if (this.finished)
            return;
        this.finished = true;
        clearTimeout(this.timer);
        this.signal?.removeEventListener("abort", this.onAbort);
        const now = performance.now();
        for (const pending of this.pending.values()) {
            if (pending.record)
                pending.record.durationMs = now - pending.startedAt;
            pending.controller.abort();
        }
        this.pending.clear();
        const result = error
            ? { ok: false, error, output: this.output, calls: this.calls }
            : {
                ok: true,
                value,
                output: this.output,
                calls: this.calls,
                storeWrites: writes ?? { set: {}, delete: [] },
            };
        if (!this.worker) {
            this.resolveResult(result);
            return;
        }
        Atomics.store(new Int32Array(this.interrupt), 0, 1);
        this.worker
            .terminate()
            .catch(() => undefined)
            .then(() => this.resolveResult(result));
    }
}
/**
 * Runs JavaScript in a QuickJS VM (a separate wasm instance) inside a worker
 * thread. The script sees `tools.<name>(args)` for every registered tool, `ALL_TOOLS`,
 * the output helpers `text`, `image`, `exit`, and `console.*`, `store`/`load`, and the
 * configured globals; nothing else (no timers, `fetch`, `process`, `require`, modules).
 *
 * Each `execute()` gets its own worker and VM; the sandbox only holds the tool
 * table and defaults. `close()` aborts in-flight executions.
 */
export class CodemodeSandbox {
    toolsByName = new Map();
    globalsByName = new Map();
    timeoutMs;
    memoryLimitBytes;
    wasm;
    workerUrl;
    running = new Set();
    closed = false;
    constructor(options = {}) {
        this.timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
        this.memoryLimitBytes = options.memoryLimitBytes;
        this.wasm = options.wasm;
        this.workerUrl = options.workerUrl ?? defaultWorkerUrl();
        for (const tool of options.tools ?? [])
            this.registerTool(tool);
        const namespaces = new Set();
        for (const global of options.globals ?? []) {
            const parts = global.name.split(".");
            if (parts.length > 2 || !parts.every((part) => IDENTIFIER.test(part)) || RESERVED_GLOBALS.has(parts[0])) {
                throw new Error(`Invalid global name "${global.name}"`);
            }
            if (this.globalsByName.has(global.name))
                throw new Error(`Global "${global.name}" is already registered`);
            if (parts.length === 2)
                namespaces.add(parts[0]);
            this.globalsByName.set(global.name, global);
        }
        for (const name of namespaces) {
            if (this.globalsByName.has(name))
                throw new Error(`Global "${name}" conflicts with the namespace "${name}"`);
        }
    }
    /** Throws if a tool with the same name is already registered. */
    registerTool(tool) {
        if (this.toolsByName.has(tool.name))
            throw new Error(`Tool "${tool.name}" is already registered`);
        this.toolsByName.set(tool.name, tool);
    }
    unregisterTool(name) {
        return this.toolsByName.delete(name);
    }
    get tools() {
        return [...this.toolsByName.values()];
    }
    get globals() {
        return [...this.globalsByName.values()];
    }
    /**
     * `code` is an async function body: `return` and top-level `await` work.
     * Never rejects for script failures; those come back as `{ ok: false }`.
     * The script can use `store(key, value)` and `load(key)` on `options.store`.
     */
    execute(code, options = {}) {
        if (this.closed)
            return Promise.reject(new Error("Sandbox is closed"));
        const execution = new Execution({
            code,
            tools: new Map(this.toolsByName),
            globals: this.globalsByName,
            timeoutMs: options.timeoutMs ?? this.timeoutMs,
            signal: options.signal,
            memoryLimitBytes: this.memoryLimitBytes,
            store: serializeStore(options.store),
            wasm: this.wasm === undefined ? loadQuickJSWasm() : Promise.resolve(this.wasm),
            workerUrl: this.workerUrl,
        });
        this.running.add(execution);
        return execution.promise.finally(() => this.running.delete(execution));
    }
    /** Aborts in-flight executions (they resolve with `kind: "aborted"`) and rejects new ones. */
    async close() {
        this.closed = true;
        await Promise.all([...this.running].map((execution) => execution.abort("Sandbox closed")));
    }
}
//# sourceMappingURL=host.js.map