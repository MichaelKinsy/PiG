import {
  Bm25Ranker,
  CODEMODE_STORE_ENTRY_TYPE,
  DEFAULT_TOOL_SEARCH_LIMIT,
  MODEL_GLOBAL_DECLARATIONS,
  createToolSearchDocument,
  getCodemodeCallableTools,
  toCodemodeDeclaration
} from "./chunk-6HFDIE7X.js";
import {
  combineUsage
} from "./chunk-M5LAR3ND.js";
import "./chunk-RUCWNNX6.js";
import {
  getCodemodeWorkerSpecifier,
  getQuickJSWasmPath
} from "./chunk-CBPXJ43O.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/extensions/codemode/execute.js
import { randomBytes } from "node:crypto";
import { writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { CodemodeSandbox, loadQuickJSWasm, parseCodemodeSource, renderToolSample, toCodemodeIdentifier } from "../../pi-codemode/index.js";
var ARGS_PREVIEW_CHARS = 200;
var ERROR_PREVIEW_CHARS = 500;
var MAX_CONCURRENT_MODEL_CALLS = 4;
var CODEMODE_MEMORY_LIMIT_BYTES = 256 * 1024 * 1024;
var MODEL_TYPES = /* @__PURE__ */ new Set(["chat", "image", "classifier"]);
function truncateText(text, maxChars) {
  return text.length > maxChars ? `${text.slice(0, maxChars - 3)}...` : text;
}
__name(truncateText, "truncateText");
function previewArgs(args) {
  if (args === void 0)
    return "";
  try {
    return truncateText(JSON.stringify(args) ?? "", ARGS_PREVIEW_CHARS);
  } catch {
    return "";
  }
}
__name(previewArgs, "previewArgs");
function textOf(result) {
  return (result.content ?? []).filter((block) => block.type === "text").map((block) => block.text).join("\n");
}
__name(textOf, "textOf");
function toModelType(value) {
  if (typeof value === "string" && MODEL_TYPES.has(value))
    return value;
  throw new Error(`Unknown model type ${JSON.stringify(value)}. Use "chat", "image", or "classifier".`);
}
__name(toModelType, "toModelType");
function toProvider(value) {
  if (value === void 0 || value === null)
    return void 0;
  if (typeof value !== "string")
    throw new Error("provider must be a string");
  return value;
}
__name(toProvider, "toProvider");
function toModelInfo(model) {
  const info = { ...model };
  delete info.headers;
  return info;
}
__name(toModelInfo, "toModelInfo");
function createLimiter(limit) {
  let active = 0;
  const waiting = [];
  return async (run) => {
    if (active >= limit)
      await new Promise((resolve) => waiting.push(resolve));
    active++;
    try {
      return await run();
    } finally {
      active--;
      waiting.shift()?.();
    }
  };
}
__name(createLimiter, "createLimiter");
function isStoreEntryData(data) {
  if (typeof data !== "object" || data === null)
    return false;
  const { set, delete: deleted } = data;
  return typeof set === "object" && set !== null && Array.isArray(deleted) && deleted.every((key) => typeof key === "string");
}
__name(isStoreEntryData, "isStoreEntryData");
function readCodemodeStore(branch) {
  const store = /* @__PURE__ */ new Map();
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
__name(readCodemodeStore, "readCodemodeStore");
var DEFAULT_MAX_OUTPUT_TOKENS = 1e4;
var CHARS_PER_TOKEN = 4;
function valueText(value) {
  if (typeof value === "string")
    return value;
  return JSON.stringify(value) ?? String(value);
}
__name(valueText, "valueText");
function formatCallSummary(calls) {
  if (calls.length === 0)
    return "No tool calls were made.";
  return `Tool calls made before the failure (they are not undone): ${calls.map((call) => `${call.name} (${call.status})`).join(", ")}`;
}
__name(formatCallSummary, "formatCallSummary");
function formatError(result, calls) {
  const { error } = result;
  const head = error.kind === "script" ? error.stack ?? `${error.name ?? "Error"}: ${error.message}` : error.kind === "timeout" ? `Script timed out: ${error.message}` : error.kind === "aborted" ? `Script aborted: ${error.message}` : `Script sandbox failed: ${error.message}`;
  return `${head}

${formatCallSummary(calls)}`;
}
__name(formatError, "formatError");
async function spillOutput(text) {
  const path = join(tmpdir(), `pi-codemode-${randomBytes(8).toString("hex")}.txt`);
  try {
    await writeFile(path, text);
    return { path };
  } catch (error) {
    return { error: error instanceof Error ? error.message : String(error) };
  }
}
__name(spillOutput, "spillOutput");
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
  let text = `Warning: truncated output (original token count: ${Math.ceil(combined.length / CHARS_PER_TOKEN)})
Total output lines: ${combined.split("\n").length}

${head}\u2026${Math.ceil(removed / CHARS_PER_TOKEN)} tokens truncated\u2026${tail}`;
  const spilled = await spillOutput(combined);
  text += "path" in spilled ? `

[Full output: ${spilled.path} (read with offset/limit)]` : `

[Could not save the full output: ${spilled.error}]`;
  return {
    items: [{ type: "text", text }, ...items.filter((item) => item.type === "image")],
    ..."path" in spilled ? { fullOutputPath: spilled.path } : {}
  };
}
__name(truncateOutput, "truncateOutput");
function toScriptValue(tool, outcome) {
  const { result } = outcome;
  if (tool.outputSchema && result.structuredContent !== void 0)
    return result.structuredContent;
  const text = textOf(result);
  if (outcome.isError)
    throw new Error(text || `Tool "${tool.name}" failed`);
  return text;
}
__name(toScriptValue, "toScriptValue");
async function executeCodemode(toolCallId, input, signal, onUpdate, ctx, options = {}) {
  const startedAt = performance.now();
  const { code, options: sourceOptions } = parseCodemodeSource(input.code);
  const calls = [];
  let modelUsage;
  const addModelUsage = /* @__PURE__ */ __name((usage) => {
    modelUsage = modelUsage ? combineUsage(modelUsage, usage) : usage;
  }, "addModelUsage");
  const snapshot = /* @__PURE__ */ __name(() => ({ calls: calls.map((call) => ({ ...call })) }), "snapshot");
  const publish = /* @__PURE__ */ __name(() => onUpdate?.({ content: [], details: snapshot() }), "publish");
  const callable = ctx ? getCodemodeCallableTools(ctx.tools) : [];
  const samples = new Map(callable.map((tool) => [tool.name, renderToolSample(toCodemodeDeclaration(tool))]));
  const sandboxTools = callable.map((tool) => ({
    name: tool.name,
    description: samples.get(tool.name),
    execute: /* @__PURE__ */ __name(async (args, { signal: callSignal }) => {
      const record = {
        id: `${toolCallId}/?`,
        name: tool.name,
        args: previewArgs(args),
        status: "running"
      };
      calls.push(record);
      publish();
      const callStartedAt = performance.now();
      if (!ctx)
        throw new Error("Tool calls need a session");
      const outcome = await ctx.executeTool(tool.name, args, { signal: callSignal });
      record.id = outcome.toolCall.id;
      record.durationMs = performance.now() - callStartedAt;
      if (outcome.isError) {
        record.status = callSignal.aborted ? "cancelled" : "error";
        record.error = truncateText(textOf(outcome.result) || `Tool "${tool.name}" failed`, ERROR_PREVIEW_CHARS);
      } else {
        record.status = "ok";
      }
      publish();
      return toScriptValue(tool, outcome);
    }, "execute")
  }));
  const sandbox = new CodemodeSandbox({
    tools: sandboxTools,
    globals: [
      ...createDiscoveryGlobals(callable, samples, options),
      ...options.models && ctx ? createModelGlobals(ctx.modelRegistry, toolCallId, calls, publish, addModelUsage) : []
    ],
    timeoutMs: sourceOptions.timeoutMs ?? Number.POSITIVE_INFINITY,
    memoryLimitBytes: CODEMODE_MEMORY_LIMIT_BYTES,
    wasm: loadQuickJSWasm(getQuickJSWasmPath()),
    workerUrl: getCodemodeWorkerSpecifier()
  });
  let result;
  try {
    const store = ctx ? readCodemodeStore(ctx.sessionManager.getBranch()) : {};
    result = await sandbox.execute(code, { signal, store });
  } finally {
    await sandbox.close();
  }
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
    if (result.value !== void 0)
      items.push({ type: "text", text: valueText(result.value) });
  } else {
    items.push({ type: "text", text: `Script error:
${formatError(result, calls)}` });
  }
  const truncated = await truncateOutput(items, sourceOptions.maxOutputTokens ?? DEFAULT_MAX_OUTPUT_TOKENS);
  const wallTime = ((performance.now() - startedAt) / 1e3).toFixed(1);
  const header = `${result.ok ? "Script completed" : "Script failed"}
Wall time ${wallTime} seconds
Output:
`;
  const details = snapshot();
  if (truncated.fullOutputPath)
    details.fullOutputPath = truncated.fullOutputPath;
  return {
    content: [{ type: "text", text: header }, ...truncated.items],
    details,
    ...modelUsage ? { usage: modelUsage } : {},
    ...result.ok ? {} : { isError: true }
  };
}
__name(executeCodemode, "executeCodemode");
function isNamespaceName(namespace, query) {
  const id = toCodemodeIdentifier(namespace);
  const queryId = toCodemodeIdentifier(query);
  const suffix = /* @__PURE__ */ __name((name) => name.includes("__") ? name.slice(name.lastIndexOf("__") + 2) : void 0, "suffix");
  return namespace === query || id === queryId || suffix(namespace) === query || suffix(id) === queryId;
}
__name(isNamespaceName, "isNamespaceName");
function createDiscoveryGlobals(tools, samples, options) {
  const ranker = new Bm25Ranker();
  const entry = /* @__PURE__ */ __name((name) => ({ name: toCodemodeIdentifier(name), description: samples.get(name) ?? "" }), "entry");
  return [
    {
      name: "searchTools",
      spread: true,
      execute: /* @__PURE__ */ __name((args) => {
        const [query, searchOptions] = args;
        if (typeof query !== "string")
          throw new Error("searchTools() expects a query string");
        const limit = searchOptions?.limit ?? DEFAULT_TOOL_SEARCH_LIMIT;
        if (typeof limit !== "number" || !Number.isInteger(limit) || limit <= 0) {
          throw new Error("searchTools() limit must be a positive integer");
        }
        const namespace = searchOptions?.namespace;
        if (namespace !== void 0 && namespace !== null && typeof namespace !== "string") {
          throw new Error("searchTools() namespace must be a string");
        }
        const documents = tools.flatMap((tool) => {
          const toolNamespace = options.getToolNamespace?.(tool.name);
          if (namespace && (!toolNamespace || !isNamespaceName(toolNamespace.name, namespace)))
            return [];
          return [createToolSearchDocument(tool, toolNamespace)];
        });
        return ranker.rank(query, documents, limit).map((match) => entry(match.name));
      }, "execute")
    },
    {
      name: "describeTool",
      spread: true,
      execute: /* @__PURE__ */ __name((args) => {
        const [name] = args;
        if (typeof name !== "string")
          throw new Error("describeTool() expects a tool name");
        const tool = tools.find((candidate) => candidate.name === name || toCodemodeIdentifier(candidate.name) === name);
        return tool ? samples.get(tool.name) : void 0;
      }, "execute")
    },
    {
      name: "describeNamespace",
      spread: true,
      execute: /* @__PURE__ */ __name((args) => {
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
          return void 0;
        return {
          name: namespace.name,
          ...namespace.description ? { description: namespace.description } : {},
          ...namespace.instructions ? { instructions: namespace.instructions } : {},
          tools: names
        };
      }, "execute")
    }
  ];
}
__name(createDiscoveryGlobals, "createDiscoveryGlobals");
function createModelGlobals(models, toolCallId, calls, publish, addUsage) {
  const limit = createLimiter(MAX_CONCURRENT_MODEL_CALLS);
  let classifyCount = 0;
  const implementations = {
    "models.getModelsOfType": /* @__PURE__ */ __name((args) => {
      const [type, provider] = args;
      return models.getModelsOfType(toModelType(type), toProvider(provider)).map(toModelInfo);
    }, "models.getModelsOfType"),
    "models.getAvailableOfType": /* @__PURE__ */ __name(async (args, { signal }) => {
      const [type, provider] = args;
      const available = await models.getAvailableOfType(toModelType(type), toProvider(provider), { signal });
      return available.map(toModelInfo);
    }, "models.getAvailableOfType"),
    "models.getModelOfType": /* @__PURE__ */ __name((args) => {
      const [type, provider, id] = args;
      if (typeof provider !== "string" || typeof id !== "string") {
        throw new Error("models.getModelOfType() expects a type, a provider, and an id");
      }
      const model = models.getModelOfType(toModelType(type), provider, id);
      return model === void 0 ? void 0 : toModelInfo(model);
    }, "models.getModelOfType"),
    "models.classify": /* @__PURE__ */ __name(async (args, { signal }) => {
      const [model, context] = args;
      const ref = model;
      if (typeof ref !== "object" || ref === null || typeof ref.provider !== "string" || typeof ref.id !== "string") {
        throw new Error("models.classify() expects a model from models.getModelOfType() or models.getAvailableOfType()");
      }
      const resolved = models.getModelOfType("classifier", ref.provider, ref.id);
      if (!resolved)
        throw new Error(`Unknown classifier model "${ref.provider}/${ref.id}"`);
      const record = {
        id: `${toolCallId}/models.classify/${++classifyCount}`,
        name: "models.classify",
        args: `${resolved.provider}/${resolved.id}`,
        status: "running"
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
    }, "models.classify")
  };
  return MODEL_GLOBAL_DECLARATIONS.map((declaration) => ({
    name: declaration.name,
    spread: true,
    execute: implementations[declaration.name]
  }));
}
__name(createModelGlobals, "createModelGlobals");
export {
  executeCodemode,
  readCodemodeStore
};
