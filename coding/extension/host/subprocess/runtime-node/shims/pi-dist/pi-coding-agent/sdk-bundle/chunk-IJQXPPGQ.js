import {
  Bm25Ranker,
  CODEMODE_DOCS_PATH,
  CODEMODE_STORE_ENTRY_TYPE,
  DEFAULT_TOOL_SEARCH_LIMIT,
  createToolSearchDocument,
  getCodemodeCallableTools,
  toCodemodeDeclaration
} from "./chunk-ILH7WEUZ.js";
import {
  combineUsage
} from "./chunk-LKHC5YTL.js";
import {
  formatSize,
  writeOutputFile
} from "./chunk-QJIK7HVQ.js";
import "./chunk-472CZNGH.js";
import "./chunk-RVJYK3FO.js";
import {
  getCodemodeWorkerSpecifier,
  getQuickJSWasmPath
} from "./chunk-YXY3JV6E.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/extensions/codemode/execute.js
import { CodemodeSandbox, loadQuickJSWasm, parseCodemodeSource, renderToolSample, toCodemodeIdentifier } from "../../pi-codemode/index.js";
var ARGS_PREVIEW_CHARS = 200;
var ERROR_PREVIEW_CHARS = 500;
var MAX_CONCURRENT_MODEL_CALLS = 4;
var CODEMODE_MEMORY_LIMIT_BYTES = 256 * 1024 * 1024;
var DEFAULT_MAX_CODEMODE_WORKERS = 2;
function maxCodemodeWorkers() {
  const value = Number(process.env.PI_CODEMODE_MAX_WORKERS);
  return Number.isInteger(value) && value > 0 ? value : DEFAULT_MAX_CODEMODE_WORKERS;
}
__name(maxCodemodeWorkers, "maxCodemodeWorkers");
var CODEMODE_WORKER_WAIT_MS = 6e4;
var activeCodemodeWorkers = 0;
var codemodeWorkerWaiters = [];
function reserveCodemodeWorker(signal, timeoutMs) {
  if (signal?.aborted)
    return Promise.resolve("aborted");
  return new Promise((resolve) => {
    const settle = /* @__PURE__ */ __name((outcome) => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      const index = codemodeWorkerWaiters.indexOf(grant);
      if (index >= 0)
        codemodeWorkerWaiters.splice(index, 1);
      resolve(outcome);
    }, "settle");
    const grant = /* @__PURE__ */ __name(() => settle("reserved"), "grant");
    const onAbort = /* @__PURE__ */ __name(() => settle("aborted"), "onAbort");
    const timer = setTimeout(() => settle("timeout"), timeoutMs);
    codemodeWorkerWaiters.push(grant);
    signal?.addEventListener("abort", onAbort, { once: true });
    admitCodemodeWorkers();
  });
}
__name(reserveCodemodeWorker, "reserveCodemodeWorker");
function admitCodemodeWorkers() {
  while (codemodeWorkerWaiters.length > 0 && activeCodemodeWorkers < maxCodemodeWorkers()) {
    activeCodemodeWorkers++;
    codemodeWorkerWaiters.shift()?.();
  }
}
__name(admitCodemodeWorkers, "admitCodemodeWorkers");
function releaseCodemodeWorker() {
  activeCodemodeWorkers--;
  admitCodemodeWorkers();
}
__name(releaseCodemodeWorker, "releaseCodemodeWorker");
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
function withArticle(word) {
  return `${/^[aeiou]/.test(word) ? "an" : "a"} ${word}`;
}
__name(withArticle, "withArticle");
function describeValue(value) {
  if (value === void 0 || value === null)
    return String(value);
  if (Array.isArray(value))
    return value.length === 0 ? "an empty array" : "an array";
  if (typeof value === "object") {
    const keys = Object.keys(value);
    if (keys.length === 0)
      return "{}";
    return `{ ${keys.slice(0, 6).join(", ")}${keys.length > 6 ? ", ..." : ""} }`;
  }
  return typeof value === "string" ? "a string" : `a ${typeof value}`;
}
__name(describeValue, "describeValue");
var CLASSIFIER_CONTEXT_SHAPE = '{ state: { ... }, images?: [{ type: "image", data: <base64>, mimeType }], questions: { <id>: { type: "choice", instructions, criteria: { <label>: <meaning> } } | { type: "score", instructions, criteria: [<lowest level>, ..., <highest level>] } | { type: "bool", instructions, criteria: { true: <meaning>, false: <meaning> } } } }';
function checkClassifierContext(context) {
  const fail = /* @__PURE__ */ __name((problem) => new Error(`models.classify() ${problem}. Expected context: ${CLASSIFIER_CONTEXT_SHAPE}. See "Classify" in ${CODEMODE_DOCS_PATH}.`), "fail");
  if (typeof context !== "object" || context === null || Array.isArray(context)) {
    throw fail(`expects a context object as its second argument, got ${describeValue(context)}`);
  }
  const { state, images, questions } = context;
  if (typeof state !== "object" || state === null || Array.isArray(state)) {
    throw fail(`context.state must be an object, got ${describeValue(state)}`);
  }
  if (images !== void 0) {
    if (!Array.isArray(images))
      throw fail(`context.images must be an array, got ${describeValue(images)}`);
    images.forEach((image, index) => {
      if (image?.type !== "image" || typeof image.data !== "string" || typeof image.mimeType !== "string") {
        throw fail(`context.images[${index}] must be an image block, got ${describeValue(image)}`);
      }
    });
  }
  if (typeof questions !== "object" || questions === null || Array.isArray(questions) || Object.keys(questions).length === 0) {
    throw fail(`context.questions must map question IDs to questions, got ${describeValue(questions)}`);
  }
  const isStrings = /* @__PURE__ */ __name((values) => values.length > 0 && values.every((value) => typeof value === "string"), "isStrings");
  for (const [id, question] of Object.entries(questions)) {
    const at = `context.questions.${id}`;
    if (typeof question !== "object" || question === null || Array.isArray(question)) {
      throw fail(`${at} must be a question object, got ${describeValue(question)}`);
    }
    const { type, instructions, criteria } = question;
    if (typeof instructions !== "string")
      throw fail(`${at}.instructions must be a string`);
    if (type === "choice") {
      if (typeof criteria !== "object" || criteria === null || Array.isArray(criteria) || !isStrings(Object.values(criteria))) {
        throw fail(`${at} is a "choice" question, so criteria must map each label to its meaning`);
      }
    } else if (type === "score") {
      if (!Array.isArray(criteria) || !isStrings(criteria)) {
        throw fail(`${at} is a "score" question, so criteria must list the levels as strings, lowest first`);
      }
    } else if (type === "bool") {
      const labels = criteria;
      if (typeof labels?.true !== "string" || typeof labels.false !== "string") {
        throw fail(`${at} is a "bool" question, so criteria must be { true: string, false: string }`);
      }
    } else {
      throw fail(`${at}.type must be "choice", "score", or "bool", got ${JSON.stringify(type)}`);
    }
  }
  return context;
}
__name(checkClassifierContext, "checkClassifierContext");
function checkImagesContext(context) {
  const fail = /* @__PURE__ */ __name((problem) => new Error(`models.generateImages() ${problem}. Expected context: { input: [{ type: "text", text: <prompt> }, ...optional { type: "image", data: <base64>, mimeType } references] }. See "Generate images" in ${CODEMODE_DOCS_PATH}.`), "fail");
  if (typeof context !== "object" || context === null || Array.isArray(context)) {
    throw fail(`expects a context object as its second argument, got ${describeValue(context)}`);
  }
  const { input } = context;
  if (!Array.isArray(input) || input.length === 0) {
    throw fail(`context.input must be a non-empty array of blocks, got ${describeValue(input)}`);
  }
  input.forEach((block, index) => {
    if (block?.type === "text" && typeof block.text === "string")
      return;
    if (block?.type === "image" && typeof block.data === "string" && typeof block.mimeType === "string")
      return;
    throw fail(`context.input[${index}] must be a text or image block, got ${describeValue(block)}`);
  });
  return context;
}
__name(checkImagesContext, "checkImagesContext");
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
function formatOutput(output) {
  const total = output.filter((item) => item.type === "text" && !item.console).length;
  const items = [];
  const consoleLines = [];
  let index = 0;
  for (const item of output) {
    if (item.type === "image") {
      items.push(item);
    } else if (item.console) {
      consoleLines.push(item.text);
    } else {
      index++;
      items.push({ type: "text", text: total > 1 ? `==> text ${index}/${total} <==
${item.text}` : item.text });
    }
  }
  if (consoleLines.length > 0) {
    items.push({ type: "text", text: `<console_output>
${consoleLines.join("\n")}
</console_output>` });
  }
  return items;
}
__name(formatOutput, "formatOutput");
function joinAdjacentText(items) {
  const joined = [];
  let parts = [];
  let lastPart = "";
  const flush = /* @__PURE__ */ __name(() => {
    if (parts.length > 0)
      joined.push({ type: "text", text: parts.join("") });
    parts = [];
    lastPart = "";
  }, "flush");
  for (const item of items) {
    if (item.type !== "text") {
      flush();
      joined.push(item);
      continue;
    }
    if (lastPart !== "" && !lastPart.endsWith("\n")) {
      parts.push("\n");
      lastPart = "\n";
    }
    parts.push(item.text);
    if (item.text !== "")
      lastPart = item.text;
  }
  flush();
  return joined;
}
__name(joinAdjacentText, "joinAdjacentText");
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
  try {
    return { path: await writeOutputFile("pi-codemode", ".txt", text) };
  } catch (error) {
    return { error: error instanceof Error ? error.message : String(error) };
  }
}
__name(spillOutput, "spillOutput");
var IMAGE_EXTENSIONS = {
  "image/png": ".png",
  "image/jpeg": ".jpg",
  "image/gif": ".gif",
  "image/webp": ".webp"
};
async function saveImages(items) {
  const labels = /* @__PURE__ */ new Map();
  const label = /* @__PURE__ */ __name(async ({ data, mimeType }) => {
    const bytes = Buffer.from(data, "base64");
    const kind = `${mimeType}, ${formatSize(bytes.length)}`;
    const extension = IMAGE_EXTENSIONS[mimeType];
    if (!extension)
      throw new Error(`No file extension for image type ${mimeType}`);
    try {
      const path = await writeOutputFile("pi-codemode", extension, bytes);
      return `[Image saved to ${path} (${kind})]`;
    } catch (error) {
      return `[Image (${kind}) could not be saved: ${error instanceof Error ? error.message : String(error)}]`;
    }
  }, "label");
  const result = await Promise.all(items.map(async (item) => {
    if (item.type !== "image")
      return [item];
    let pending = labels.get(item.data);
    if (!pending) {
      pending = label(item);
      labels.set(item.data, pending);
    }
    return [{ type: "text", text: await pending }, item];
  }));
  return result.flat();
}
__name(saveImages, "saveImages");
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
  let generatedImages = 0;
  const addGeneratedImages = /* @__PURE__ */ __name((count) => {
    generatedImages += count;
  }, "addGeneratedImages");
  const addModelUsage = /* @__PURE__ */ __name((usage) => {
    modelUsage = modelUsage ? combineUsage(modelUsage, usage) : usage;
  }, "addModelUsage");
  const snapshot = /* @__PURE__ */ __name(() => ({ calls: calls.map((call) => ({ ...call })) }), "snapshot");
  const publish = /* @__PURE__ */ __name(() => onUpdate?.({ content: [], details: snapshot() }), "publish");
  publish();
  const callable = ctx ? getCodemodeCallableTools(ctx.tools) : [];
  const guidelines = options.getToolGuidelines?.();
  const samples = new Map(callable.map((tool) => [tool.name, renderToolSample(toCodemodeDeclaration(tool, guidelines?.get(tool.name)))]));
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
  const deadline = sourceOptions.timeoutMs ?? Number.POSITIVE_INFINITY;
  const waitStartedAt = performance.now();
  const admission = await reserveCodemodeWorker(signal, Math.min(deadline, CODEMODE_WORKER_WAIT_MS));
  const waitedMs = performance.now() - waitStartedAt;
  if (admission !== "reserved") {
    const waited = ((performance.now() - startedAt) / 1e3).toFixed(1);
    const reason = signal?.reason;
    const error = admission === "aborted" ? `Script aborted: ${reason instanceof Error ? reason.message : "Execution aborted"}` : deadline <= CODEMODE_WORKER_WAIT_MS ? `Script timed out: Execution timed out after ${deadline} ms while waiting for a free codemode worker` : `No codemode worker became free within ${CODEMODE_WORKER_WAIT_MS / 1e3} seconds. All ${maxCodemodeWorkers()} workers, shared by every session in this process, are held by other scripts. Retry after they finish.`;
    return {
      content: [
        { type: "text", text: `Script failed
Wall time ${waited} seconds
Output:
` },
        { type: "text", text: `Script error:
${error}

The script did not start.` }
      ],
      details: snapshot(),
      isError: true
    };
  }
  let reserved = true;
  let sandbox;
  let result;
  try {
    sandbox = new CodemodeSandbox({
      tools: sandboxTools,
      globals: [
        ...createDiscoveryGlobals(callable, samples, options),
        ...options.models && ctx ? createModelGlobals(ctx.modelRegistry, toolCallId, calls, publish, addModelUsage, addGeneratedImages) : []
      ],
      timeoutMs: Math.max(1, Math.round(deadline - waitedMs)),
      memoryLimitBytes: CODEMODE_MEMORY_LIMIT_BYTES,
      wasm: loadQuickJSWasm(getQuickJSWasmPath()),
      workerUrl: getCodemodeWorkerSpecifier(),
      acquireWorker: /* @__PURE__ */ __name(() => {
        if (!reserved)
          throw new Error("The script already settled");
        reserved = false;
        return releaseCodemodeWorker;
      }, "acquireWorker")
    });
    const store = ctx ? readCodemodeStore(ctx.sessionManager.getBranch()) : {};
    result = await sandbox.execute(code, { signal, store });
  } finally {
    await sandbox?.close();
    if (reserved) {
      reserved = false;
      releaseCodemodeWorker();
    }
  }
  for (const call of calls) {
    if (call.status === "running")
      call.status = "cancelled";
  }
  const scriptOutput = [...result.output];
  if (result.ok) {
    const { set, delete: deleted } = result.storeWrites;
    if (Object.keys(set).length > 0 || deleted.length > 0) {
      options.appendEntry?.(CODEMODE_STORE_ENTRY_TYPE, { set, delete: deleted });
    }
    if (result.value !== void 0)
      scriptOutput.push({ type: "text", text: valueText(result.value) });
  }
  const items = formatOutput(scriptOutput);
  if (!result.ok)
    items.push({ type: "text", text: `Script error:
${formatError(result, calls)}` });
  if (generatedImages > 0 && !items.some((item) => item.type === "image")) {
    items.push({
      type: "text",
      text: `Note: models.generateImages() returned ${generatedImages} image${generatedImages === 1 ? "" : "s"} that the script did not show. Show each image block of result.output with image(block).`
    });
  }
  const truncated = await truncateOutput(joinAdjacentText(items), sourceOptions.maxOutputTokens ?? DEFAULT_MAX_OUTPUT_TOKENS);
  const output = joinAdjacentText(await saveImages(truncated.items));
  const wallTime = ((performance.now() - startedAt) / 1e3).toFixed(1);
  const header = `${result.ok ? "Script completed" : "Script failed"}
Wall time ${wallTime} seconds
Output:
`;
  const details = snapshot();
  if (truncated.fullOutputPath)
    details.fullOutputPath = truncated.fullOutputPath;
  return {
    content: [{ type: "text", text: header }, ...output],
    details,
    ...modelUsage ? { usage: modelUsage } : {},
    ...result.ok ? {} : { isError: true }
  };
}
__name(executeCodemode, "executeCodemode");
function isNamespaceName(namespace, query) {
  const queryId = toCodemodeIdentifier(query);
  return [namespace.name, ...namespace.aliases ?? []].some((name) => name === query || toCodemodeIdentifier(name) === queryId);
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
          if (namespace && (!toolNamespace || !isNamespaceName(toolNamespace, namespace)))
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
          if (!toolNamespace || !isNamespaceName(toolNamespace, name))
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
function createModelGlobals(models, toolCallId, calls, publish, addUsage, addGeneratedImages) {
  const limit = createLimiter(MAX_CONCURRENT_MODEL_CALLS);
  let callCount = 0;
  const runModelCall = /* @__PURE__ */ __name(async (name, type, [model, context], checkContext, run) => {
    const listHint = `List the ${type} models you can use with models.getAvailableOfType("${type}").`;
    const given = model;
    if (typeof given?.provider !== "string" || typeof given.id !== "string") {
      const undefinedHint = model === void 0 || model === null ? " models.getModelOfType() returns undefined for an unknown provider or id." : "";
      throw new Error(`${name}() expects ${withArticle(type)} model as its first argument, got ${describeValue(model)}.${undefinedHint} ${listHint}`);
    }
    const provider = given.provider;
    const id = given.id;
    const ref = `${provider}/${id}`;
    const resolved = models.getModelOfType(type, provider, id);
    if (!resolved) {
      const actualType = [...MODEL_TYPES].find((other) => other !== type && models.getModelOfType(other, provider, id) !== void 0);
      throw new Error(actualType ? `"${ref}" is ${withArticle(actualType)} model, not ${withArticle(type)} model. ${listHint}` : `Unknown ${type} model "${ref}". ${listHint}`);
    }
    const checked = checkContext(context);
    const record = {
      id: `${toolCallId}/${name}/${++callCount}`,
      name,
      args: `${resolved.provider}/${resolved.id}`,
      status: "running"
    };
    calls.push(record);
    publish();
    const startedAt = performance.now();
    const result = await limit(() => run(resolved, checked));
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
  }, "runModelCall");
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
        throw new Error(`models.getModelOfType(type, provider, id) expects three strings, got (${args.map(describeValue).join(", ")}). The provider and the id are separate arguments, for example models.getModelOfType("classifier", "typesafe", "jev-latest").`);
      }
      const model = models.getModelOfType(toModelType(type), provider, id);
      return model === void 0 ? void 0 : toModelInfo(model);
    }, "models.getModelOfType"),
    "models.classify": /* @__PURE__ */ __name((args, { signal }) => runModelCall("models.classify", "classifier", args, checkClassifierContext, (resolved, context) => models.classify(resolved, context, { signal })), "models.classify"),
    "models.generateImages": /* @__PURE__ */ __name((args, { signal }) => runModelCall("models.generateImages", "image", args, checkImagesContext, async (resolved, context) => {
      const result = await models.generateImages(resolved, context, { signal });
      addGeneratedImages(result.output.filter((block) => block.type === "image").length);
      return result;
    }), "models.generateImages")
  };
  return Object.entries(implementations).map(([name, execute]) => ({ name, spread: true, execute }));
}
__name(createModelGlobals, "createModelGlobals");
export {
  executeCodemode,
  readCodemodeStore
};
