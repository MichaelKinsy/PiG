/**
 * Runs one codemode script in the sandbox. Split from tool.ts and loaded through
 * execute.lazy.ts so the sandbox runtime only loads when a script runs.
 */
import { randomBytes } from "node:crypto";
import { writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { CodemodeSandbox, loadQuickJSWasm, parseCodemodeSource, renderToolSample, toCodemodeIdentifier, } from "../../../pi-codemode/index.js";
import { getCodemodeWorkerSpecifier, getQuickJSWasmPath } from "../../config.js";
import { combineUsage } from "../../core/usage-totals.js";
import { Bm25Ranker, createToolSearchDocument, DEFAULT_TOOL_SEARCH_LIMIT } from "../tool-search/tool.js";
import { CODEMODE_STORE_ENTRY_TYPE, getCodemodeCallableTools, MODEL_GLOBAL_DECLARATIONS, toCodemodeDeclaration, } from "./tool.js";
const ARGS_PREVIEW_CHARS = 200;
const ERROR_PREVIEW_CHARS = 500;
/** Classifier calls one script may have in flight; `Promise.all` over many items queues the rest. */
const MAX_CONCURRENT_MODEL_CALLS = 4;
/**
 * Heap limit for the QuickJS VM. The worker shares pi's process, so without a limit a runaway
 * script can grow to wasm32's 4 GiB and take the session down. Overruns throw
 * `InternalError: out of memory` inside the script.
 */
const CODEMODE_MEMORY_LIMIT_BYTES = 256 * 1024 * 1024;
const MODEL_TYPES = new Set(["chat", "image", "classifier"]);
function truncateText(text, maxChars) {
    return text.length > maxChars ? `${text.slice(0, maxChars - 3)}...` : text;
}
function previewArgs(args) {
    if (args === undefined)
        return "";
    try {
        return truncateText(JSON.stringify(args) ?? "", ARGS_PREVIEW_CHARS);
    }
    catch {
        return "";
    }
}
function textOf(result) {
    return (result.content ?? [])
        .filter((block) => block.type === "text")
        .map((block) => block.text)
        .join("\n");
}
function toModelType(value) {
    if (typeof value === "string" && MODEL_TYPES.has(value))
        return value;
    throw new Error(`Unknown model type ${JSON.stringify(value)}. Use "chat", "image", or "classifier".`);
}
function toProvider(value) {
    if (value === undefined || value === null)
        return undefined;
    if (typeof value !== "string")
        throw new Error("provider must be a string");
    return value;
}
/** Catalog entry for scripts. `headers` is dropped because models.json headers can carry credentials. */
function toModelInfo(model) {
    const info = { ...model };
    delete info.headers;
    return info;
}
/** Runs at most `limit` calls at once, in call order. */
function createLimiter(limit) {
    let active = 0;
    const waiting = [];
    return async (run) => {
        if (active >= limit)
            await new Promise((resolve) => waiting.push(resolve));
        active++;
        try {
            return await run();
        }
        finally {
            active--;
            waiting.shift()?.();
        }
    };
}
function isStoreEntryData(data) {
    if (typeof data !== "object" || data === null)
        return false;
    const { set, delete: deleted } = data;
    return (typeof set === "object" &&
        set !== null &&
        Array.isArray(deleted) &&
        deleted.every((key) => typeof key === "string"));
}
/** Values of `load()`: the `codemode-store` entries on the branch, applied from the root. */
export function readCodemodeStore(branch) {
    const store = new Map();
    for (const entry of branch) {
        if (entry.type !== "custom" || entry.customType !== CODEMODE_STORE_ENTRY_TYPE || !isStoreEntryData(entry.data)) {
            continue;
        }
        for (const key of entry.data.delete)
            store.delete(key);
        for (const [key, value] of Object.entries(entry.data.set))
            store.set(key, value);
    }
    return Object.fromEntries(store);
}
/** Default token budget for script output. */
const DEFAULT_MAX_OUTPUT_TOKENS = 10_000;
/** Characters per token when estimating. */
const CHARS_PER_TOKEN = 4;
/** Like the script's `text()`: strings as is, other values as compact JSON. */
function valueText(value) {
    if (typeof value === "string")
        return value;
    return JSON.stringify(value) ?? String(value);
}
function formatCallSummary(calls) {
    if (calls.length === 0)
        return "No tool calls were made.";
    return `Tool calls made before the failure (they are not undone): ${calls.map((call) => `${call.name} (${call.status})`).join(", ")}`;
}
function formatError(result, calls) {
    const { error } = result;
    const head = error.kind === "script"
        ? (error.stack ?? `${error.name ?? "Error"}: ${error.message}`)
        : error.kind === "timeout"
            ? `Script timed out: ${error.message}`
            : error.kind === "aborted"
                ? `Script aborted: ${error.message}`
                : `Script sandbox failed: ${error.message}`;
    return `${head}\n\n${formatCallSummary(calls)}`;
}
/** Write the full text output to a temp file, like bash does for truncated output. */
async function spillOutput(text) {
    const path = join(tmpdir(), `pi-codemode-${randomBytes(8).toString("hex")}.txt`);
    try {
        await writeFile(path, text);
        return { path };
    }
    catch (error) {
        return { error: error instanceof Error ? error.message : String(error) };
    }
}
/**
 * Apply the token budget: when the combined text exceeds it, the text items become one
 * item that keeps the start and end of the text, and images follow it. The full text is written to
 * a temp file.
 */
async function truncateOutput(items, maxTokens) {
    const texts = items.filter((item) => item.type === "text").map((item) => item.text);
    const combined = texts.join("\n");
    const budget = maxTokens * CHARS_PER_TOKEN;
    if (texts.length === 0 || combined.length <= budget)
        return { items };
    const headChars = Math.floor(budget / 2);
    const tailChars = budget - headChars;
    const removed = combined.length - headChars - tailChars;
    const head = combined.slice(0, headChars);
    const tail = tailChars > 0 ? combined.slice(-tailChars) : "";
    let text = `Warning: truncated output (original token count: ${Math.ceil(combined.length / CHARS_PER_TOKEN)})\nTotal output lines: ${combined.split("\n").length}\n\n${head}…${Math.ceil(removed / CHARS_PER_TOKEN)} tokens truncated…${tail}`;
    const spilled = await spillOutput(combined);
    text +=
        "path" in spilled
            ? `\n\n[Full output: ${spilled.path} (read with offset/limit)]`
            : `\n\n[Could not save the full output: ${spilled.error}]`;
    return {
        items: [{ type: "text", text }, ...items.filter((item) => item.type === "image")],
        ...("path" in spilled ? { fullOutputPath: spilled.path } : {}),
    };
}
/**
 * The value a script receives for a nested call: a tool that declares
 * `outputSchema` resolves to its `structuredContent`, also for error results that carry one (such
 * as MCP results with `isError`); any other tool resolves to its text content. Other failures
 * reject with the tool's error text.
 */
function toScriptValue(tool, outcome) {
    const { result } = outcome;
    if (tool.outputSchema && result.structuredContent !== undefined)
        return result.structuredContent;
    const text = textOf(result);
    if (outcome.isError)
        throw new Error(text || `Tool "${tool.name}" failed`);
    return text;
}
/**
 * Run one script. Without a session context (a plain Agent or a direct call) scripts cannot call
 * tools, `store()` starts empty, and writes are dropped.
 */
export async function executeCodemode(toolCallId, input, signal, onUpdate, ctx, options = {}) {
    const startedAt = performance.now();
    const { code, options: sourceOptions } = parseCodemodeSource(input.code);
    const calls = [];
    // Usage of the script's `models.*` calls. Nested tool calls report theirs through the session.
    let modelUsage;
    const addModelUsage = (usage) => {
        modelUsage = modelUsage ? combineUsage(modelUsage, usage) : usage;
    };
    const snapshot = () => ({ calls: calls.map((call) => ({ ...call })) });
    const publish = () => onUpdate?.({ content: [], details: snapshot() });
    const callable = ctx ? getCodemodeCallableTools(ctx.tools) : [];
    // ALL_TOOLS entries carry the declaration.
    const samples = new Map(callable.map((tool) => [tool.name, renderToolSample(toCodemodeDeclaration(tool))]));
    const sandboxTools = callable.map((tool) => ({
        name: tool.name,
        description: samples.get(tool.name),
        execute: async (args, { signal: callSignal }) => {
            const record = {
                id: `${toolCallId}/?`,
                name: tool.name,
                args: previewArgs(args),
                status: "running",
            };
            calls.push(record);
            publish();
            const callStartedAt = performance.now();
            // Only tools from ctx.tools are callable, so ctx is set here.
            if (!ctx)
                throw new Error("Tool calls need a session");
            const outcome = await ctx.executeTool(tool.name, args, { signal: callSignal });
            record.id = outcome.toolCall.id;
            record.durationMs = performance.now() - callStartedAt;
            if (outcome.isError) {
                record.status = callSignal.aborted ? "cancelled" : "error";
                record.error = truncateText(textOf(outcome.result) || `Tool "${tool.name}" failed`, ERROR_PREVIEW_CHARS);
            }
            else {
                record.status = "ok";
            }
            publish();
            return toScriptValue(tool, outcome);
        },
    }));
    const sandbox = new CodemodeSandbox({
        tools: sandboxTools,
        globals: [
            ...createDiscoveryGlobals(callable, samples, options),
            ...(options.models && ctx
                ? createModelGlobals(ctx.modelRegistry, toolCallId, calls, publish, addModelUsage)
                : []),
        ],
        timeoutMs: sourceOptions.timeoutMs ?? Number.POSITIVE_INFINITY,
        memoryLimitBytes: CODEMODE_MEMORY_LIMIT_BYTES,
        wasm: loadQuickJSWasm(getQuickJSWasmPath()),
        workerUrl: getCodemodeWorkerSpecifier(),
    });
    let result;
    try {
        const store = ctx ? readCodemodeStore(ctx.sessionManager.getBranch()) : {};
        result = await sandbox.execute(code, { signal, store });
    }
    finally {
        await sandbox.close();
    }
    // Calls still marked running were cut off by the script ending, a timeout, or an abort.
    for (const call of calls) {
        if (call.status === "running")
            call.status = "cancelled";
    }
    const items = result.output.map((item) => item.type === "text" ? { type: "text", text: item.text } : item);
    if (result.ok) {
        const { set, delete: deleted } = result.storeWrites;
        if (Object.keys(set).length > 0 || deleted.length > 0) {
            options.appendEntry?.(CODEMODE_STORE_ENTRY_TYPE, { set, delete: deleted });
        }
        // pi extension: a returned value is appended like text().
        if (result.value !== undefined)
            items.push({ type: "text", text: valueText(result.value) });
    }
    else {
        items.push({ type: "text", text: `Script error:\n${formatError(result, calls)}` });
    }
    const truncated = await truncateOutput(items, sourceOptions.maxOutputTokens ?? DEFAULT_MAX_OUTPUT_TOKENS);
    const wallTime = ((performance.now() - startedAt) / 1000).toFixed(1);
    const header = `${result.ok ? "Script completed" : "Script failed"}\nWall time ${wallTime} seconds\nOutput:\n`;
    const details = snapshot();
    if (truncated.fullOutputPath)
        details.fullOutputPath = truncated.fullOutputPath;
    return {
        content: [{ type: "text", text: header }, ...truncated.items],
        details,
        ...(modelUsage ? { usage: modelUsage } : {}),
        ...(result.ok ? {} : { isError: true }),
    };
}
/**
 * Whether `query` names the namespace: its name, its script identifier (`mcp__dev-radius` is
 * `mcp__dev_radius`), or the part after its last `__` in either form (`dev-radius`, `dev_radius`).
 */
function isNamespaceName(namespace, query) {
    const id = toCodemodeIdentifier(namespace);
    const queryId = toCodemodeIdentifier(query);
    const suffix = (name) => (name.includes("__") ? name.slice(name.lastIndexOf("__") + 2) : undefined);
    return namespace === query || id === queryId || suffix(namespace) === query || suffix(id) === queryId;
}
/**
 * `searchTools()`, `describeTool()`, and `describeNamespace()`: ranked search and lookup over the
 * script's nested tools and their namespaces.
 */
function createDiscoveryGlobals(tools, samples, options) {
    const ranker = new Bm25Ranker();
    const entry = (name) => ({ name: toCodemodeIdentifier(name), description: samples.get(name) ?? "" });
    return [
        {
            name: "searchTools",
            spread: true,
            execute: (args) => {
                const [query, searchOptions] = args;
                if (typeof query !== "string")
                    throw new Error("searchTools() expects a query string");
                const limit = searchOptions?.limit ?? DEFAULT_TOOL_SEARCH_LIMIT;
                if (typeof limit !== "number" || !Number.isInteger(limit) || limit <= 0) {
                    throw new Error("searchTools() limit must be a positive integer");
                }
                const namespace = searchOptions?.namespace;
                if (namespace !== undefined && namespace !== null && typeof namespace !== "string") {
                    throw new Error("searchTools() namespace must be a string");
                }
                const documents = tools.flatMap((tool) => {
                    const toolNamespace = options.getToolNamespace?.(tool.name);
                    if (namespace && (!toolNamespace || !isNamespaceName(toolNamespace.name, namespace)))
                        return [];
                    return [createToolSearchDocument(tool, toolNamespace)];
                });
                return ranker.rank(query, documents, limit).map((match) => entry(match.name));
            },
        },
        {
            name: "describeTool",
            spread: true,
            execute: (args) => {
                const [name] = args;
                if (typeof name !== "string")
                    throw new Error("describeTool() expects a tool name");
                const tool = tools.find((candidate) => candidate.name === name || toCodemodeIdentifier(candidate.name) === name);
                return tool ? samples.get(tool.name) : undefined;
            },
        },
        {
            name: "describeNamespace",
            spread: true,
            execute: (args) => {
                const [name] = args;
                if (typeof name !== "string")
                    throw new Error("describeNamespace() expects a namespace name");
                let namespace;
                const names = [];
                for (const tool of tools) {
                    const toolNamespace = options.getToolNamespace?.(tool.name);
                    if (!toolNamespace || !isNamespaceName(toolNamespace.name, name))
                        continue;
                    namespace ??= toolNamespace;
                    names.push(toCodemodeIdentifier(tool.name));
                }
                if (!namespace)
                    return undefined;
                return {
                    name: namespace.name,
                    ...(namespace.description ? { description: namespace.description } : {}),
                    ...(namespace.instructions ? { instructions: namespace.instructions } : {}),
                    tools: names,
                };
            },
        },
    ];
}
/**
 * `models.*` for scripts: the model registry methods declared in {@link MODEL_GLOBAL_DECLARATIONS}.
 * Classifier calls appear as nested call rows so the renderer shows them, and their usage goes to
 * `addUsage`.
 */
function createModelGlobals(models, toolCallId, calls, publish, addUsage) {
    const limit = createLimiter(MAX_CONCURRENT_MODEL_CALLS);
    let classifyCount = 0;
    const implementations = {
        "models.getModelsOfType": (args) => {
            const [type, provider] = args;
            return models.getModelsOfType(toModelType(type), toProvider(provider)).map(toModelInfo);
        },
        "models.getAvailableOfType": async (args, { signal }) => {
            const [type, provider] = args;
            const available = await models.getAvailableOfType(toModelType(type), toProvider(provider), { signal });
            return available.map(toModelInfo);
        },
        "models.getModelOfType": (args) => {
            const [type, provider, id] = args;
            if (typeof provider !== "string" || typeof id !== "string") {
                throw new Error("models.getModelOfType() expects a type, a provider, and an id");
            }
            const model = models.getModelOfType(toModelType(type), provider, id);
            return model === undefined ? undefined : toModelInfo(model);
        },
        "models.classify": async (args, { signal }) => {
            const [model, context] = args;
            const ref = model;
            if (typeof ref !== "object" ||
                ref === null ||
                typeof ref.provider !== "string" ||
                typeof ref.id !== "string") {
                throw new Error("models.classify() expects a model from models.getModelOfType() or models.getAvailableOfType()");
            }
            // Only provider and id count. A script-supplied baseUrl or headers must never receive the credentials.
            const resolved = models.getModelOfType("classifier", ref.provider, ref.id);
            if (!resolved)
                throw new Error(`Unknown classifier model "${ref.provider}/${ref.id}"`);
            const record = {
                id: `${toolCallId}/models.classify/${++classifyCount}`,
                name: "models.classify",
                args: `${resolved.provider}/${resolved.id}`,
                status: "running",
            };
            calls.push(record);
            publish();
            const startedAt = performance.now();
            const result = await limit(() => models.classify(resolved, context, { signal }));
            record.durationMs = performance.now() - startedAt;
            record.status = result.stopReason === "stop" ? "ok" : result.stopReason === "aborted" ? "cancelled" : "error";
            if (result.errorMessage)
                record.error = truncateText(result.errorMessage, ERROR_PREVIEW_CHARS);
            if (result.usage) {
                record.cost = result.usage.cost.total;
                addUsage(result.usage);
            }
            publish();
            return result;
        },
    };
    return MODEL_GLOBAL_DECLARATIONS.map((declaration) => ({
        name: declaration.name,
        spread: true,
        execute: implementations[declaration.name],
    }));
}
//# sourceMappingURL=execute.js.map