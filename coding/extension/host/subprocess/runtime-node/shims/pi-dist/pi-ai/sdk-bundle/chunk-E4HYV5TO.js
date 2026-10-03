import {
  createProvider,
  getSystemMessageText
} from "./chunk-JUG7GWVD.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/index.js
import { Type as Type2 } from "../../../typebox.mjs";
import { lazyStream, lazyApi } from "../api/lazy.js";

// pi-dist/pi-ai/providers/faux.js
import { createAssistantMessageEventStream } from "../utils/event-stream.js";
var DEFAULT_API = "faux";
var DEFAULT_PROVIDER = "faux";
var DEFAULT_MODEL_ID = "faux-1";
var DEFAULT_MODEL_NAME = "Faux Model";
var DEFAULT_BASE_URL = "http://localhost:0";
var DEFAULT_MIN_TOKEN_SIZE = 3;
var DEFAULT_MAX_TOKEN_SIZE = 5;
var DEFAULT_USAGE = {
  input: 0,
  output: 0,
  cacheRead: 0,
  cacheWrite: 0,
  totalTokens: 0,
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }
};
function fauxText(text) {
  return { type: "text", text };
}
__name(fauxText, "fauxText");
function fauxThinking(thinking) {
  return { type: "thinking", thinking };
}
__name(fauxThinking, "fauxThinking");
function fauxToolCall(name, arguments_, options = {}) {
  return {
    type: "toolCall",
    id: options.id ?? randomId("tool"),
    name,
    arguments: arguments_
  };
}
__name(fauxToolCall, "fauxToolCall");
function normalizeFauxAssistantContent(content) {
  if (typeof content === "string") {
    return [fauxText(content)];
  }
  return Array.isArray(content) ? content : [content];
}
__name(normalizeFauxAssistantContent, "normalizeFauxAssistantContent");
function fauxAssistantMessage(content, options = {}) {
  return {
    role: "assistant",
    content: normalizeFauxAssistantContent(content),
    api: DEFAULT_API,
    provider: DEFAULT_PROVIDER,
    model: DEFAULT_MODEL_ID,
    usage: DEFAULT_USAGE,
    stopReason: options.stopReason ?? "stop",
    ...options.deferred === void 0 ? {} : { deferred: options.deferred },
    ...options.errorMessage === void 0 ? {} : { errorMessage: options.errorMessage },
    ...options.responseId === void 0 ? {} : { responseId: options.responseId },
    timestamp: options.timestamp ?? Date.now()
  };
}
__name(fauxAssistantMessage, "fauxAssistantMessage");
function estimateTokens(text) {
  return Math.ceil(text.length / 4);
}
__name(estimateTokens, "estimateTokens");
function randomId(prefix) {
  return `${prefix}:${Date.now()}:${Math.random().toString(36).slice(2)}`;
}
__name(randomId, "randomId");
function contentToText(content) {
  if (typeof content === "string") {
    return content;
  }
  return content.map((block) => {
    if (block.type === "text") {
      return block.text;
    }
    return `[image:${block.mimeType}:${block.data.length}]`;
  }).join("\n");
}
__name(contentToText, "contentToText");
function assistantContentToText(content) {
  return content.map((block) => {
    if (block.type === "text") {
      return block.text;
    }
    if (block.type === "thinking") {
      return block.thinking;
    }
    return `${block.name}:${JSON.stringify(block.arguments)}`;
  }).join("\n");
}
__name(assistantContentToText, "assistantContentToText");
function toolResultToText(message) {
  return [message.toolName, ...message.content.map((block) => contentToText([block]))].join("\n");
}
__name(toolResultToText, "toolResultToText");
function messageToText(message) {
  if (message.role === "system") {
    return [
      getSystemMessageText(message),
      ...message.toolsRemoved?.map((tool) => `tool-:${JSON.stringify(tool)}`) ?? [],
      ...message.toolsAdded?.map((tool) => `tool+:${JSON.stringify(tool)}`) ?? []
    ].filter((part) => part.length > 0).join("\n");
  }
  if (message.role === "user") {
    return contentToText(message.content);
  }
  if (message.role === "assistant") {
    return assistantContentToText(message.content);
  }
  return toolResultToText(message);
}
__name(messageToText, "messageToText");
function serializeContext(context) {
  return context.messages.map((message) => `${message.role}:${messageToText(message)}`).join("\n\n");
}
__name(serializeContext, "serializeContext");
function commonPrefixLength(a, b) {
  const length = Math.min(a.length, b.length);
  let index = 0;
  while (index < length && a[index] === b[index]) {
    index++;
  }
  return index;
}
__name(commonPrefixLength, "commonPrefixLength");
function withUsageEstimate(message, context, options, promptCache) {
  const promptText = serializeContext(context);
  const promptTokens = estimateTokens(promptText);
  const outputTokens = estimateTokens(assistantContentToText(message.content));
  let input = promptTokens;
  let cacheRead = 0;
  let cacheWrite = 0;
  const sessionId = options?.sessionId;
  if (sessionId && options?.cacheRetention !== "none") {
    const previousPrompt = promptCache.get(sessionId);
    if (previousPrompt) {
      const cachedChars = commonPrefixLength(previousPrompt, promptText);
      cacheRead = estimateTokens(previousPrompt.slice(0, cachedChars));
      cacheWrite = estimateTokens(promptText.slice(cachedChars));
      input = Math.max(0, promptTokens - cacheRead);
    } else {
      cacheWrite = promptTokens;
    }
    promptCache.set(sessionId, promptText);
  }
  return {
    ...message,
    usage: {
      input,
      output: outputTokens,
      cacheRead,
      cacheWrite,
      totalTokens: input + outputTokens + cacheRead + cacheWrite,
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 }
    }
  };
}
__name(withUsageEstimate, "withUsageEstimate");
function splitStringByTokenSize(text, minTokenSize, maxTokenSize) {
  const chunks = [];
  let index = 0;
  while (index < text.length) {
    const tokenSize = minTokenSize + Math.floor(Math.random() * (maxTokenSize - minTokenSize + 1));
    const charSize = Math.max(1, tokenSize * 4);
    chunks.push(text.slice(index, index + charSize));
    index += charSize;
  }
  return chunks.length > 0 ? chunks : [""];
}
__name(splitStringByTokenSize, "splitStringByTokenSize");
function cloneMessage(message, api, provider, modelId) {
  const cloned = structuredClone(message);
  return {
    ...cloned,
    api,
    provider,
    model: modelId,
    timestamp: cloned.timestamp ?? Date.now(),
    usage: cloned.usage ?? DEFAULT_USAGE
  };
}
__name(cloneMessage, "cloneMessage");
function createDeferredMessage(model, handle) {
  return {
    role: "assistant",
    content: [],
    api: model.api,
    provider: model.provider,
    model: model.id,
    usage: DEFAULT_USAGE,
    stopReason: "deferred",
    deferred: handle,
    timestamp: Date.now()
  };
}
__name(createDeferredMessage, "createDeferredMessage");
function createErrorMessage(error, api, provider, modelId) {
  return {
    role: "assistant",
    content: [],
    api,
    provider,
    model: modelId,
    usage: DEFAULT_USAGE,
    stopReason: "error",
    errorMessage: error instanceof Error ? error.message : String(error),
    timestamp: Date.now()
  };
}
__name(createErrorMessage, "createErrorMessage");
function createAbortedMessage(partial) {
  return {
    ...partial,
    stopReason: "aborted",
    errorMessage: "Request was aborted",
    timestamp: Date.now()
  };
}
__name(createAbortedMessage, "createAbortedMessage");
function scheduleChunk(chunk, tokensPerSecond) {
  if (!tokensPerSecond || tokensPerSecond <= 0) {
    return new Promise((resolve) => queueMicrotask(resolve));
  }
  const delayMs = estimateTokens(chunk) / tokensPerSecond * 1e3;
  return new Promise((resolve) => setTimeout(resolve, delayMs));
}
__name(scheduleChunk, "scheduleChunk");
async function streamWithDeltas(stream, message, minTokenSize, maxTokenSize, tokensPerSecond, signal) {
  const partial = { ...message, content: [], stopReason: "pending" };
  if (signal?.aborted) {
    const aborted = createAbortedMessage(partial);
    stream.push({ type: "error", reason: "aborted", error: aborted });
    stream.end(aborted);
    return;
  }
  stream.push({ type: "start", partial: { ...partial } });
  for (let index = 0; index < message.content.length; index++) {
    if (signal?.aborted) {
      const aborted = createAbortedMessage(partial);
      stream.push({ type: "error", reason: "aborted", error: aborted });
      stream.end(aborted);
      return;
    }
    const block = message.content[index];
    if (block.type === "thinking") {
      partial.content = [...partial.content, { type: "thinking", thinking: "" }];
      stream.push({ type: "thinking_start", contentIndex: index, partial: { ...partial } });
      for (const chunk of splitStringByTokenSize(block.thinking, minTokenSize, maxTokenSize)) {
        await scheduleChunk(chunk, tokensPerSecond);
        if (signal?.aborted) {
          const aborted = createAbortedMessage(partial);
          stream.push({ type: "error", reason: "aborted", error: aborted });
          stream.end(aborted);
          return;
        }
        partial.content[index].thinking += chunk;
        stream.push({ type: "thinking_delta", contentIndex: index, delta: chunk, partial: { ...partial } });
      }
      stream.push({
        type: "thinking_end",
        contentIndex: index,
        content: block.thinking,
        partial: { ...partial }
      });
      continue;
    }
    if (block.type === "text") {
      partial.content = [...partial.content, { type: "text", text: "" }];
      stream.push({ type: "text_start", contentIndex: index, partial: { ...partial } });
      for (const chunk of splitStringByTokenSize(block.text, minTokenSize, maxTokenSize)) {
        await scheduleChunk(chunk, tokensPerSecond);
        if (signal?.aborted) {
          const aborted = createAbortedMessage(partial);
          stream.push({ type: "error", reason: "aborted", error: aborted });
          stream.end(aborted);
          return;
        }
        partial.content[index].text += chunk;
        stream.push({ type: "text_delta", contentIndex: index, delta: chunk, partial: { ...partial } });
      }
      stream.push({ type: "text_end", contentIndex: index, content: block.text, partial: { ...partial } });
      continue;
    }
    partial.content = [...partial.content, { type: "toolCall", id: block.id, name: block.name, arguments: {} }];
    stream.push({ type: "toolcall_start", contentIndex: index, partial: { ...partial } });
    for (const chunk of splitStringByTokenSize(JSON.stringify(block.arguments), minTokenSize, maxTokenSize)) {
      await scheduleChunk(chunk, tokensPerSecond);
      if (signal?.aborted) {
        const aborted = createAbortedMessage(partial);
        stream.push({ type: "error", reason: "aborted", error: aborted });
        stream.end(aborted);
        return;
      }
      stream.push({ type: "toolcall_delta", contentIndex: index, delta: chunk, partial: { ...partial } });
    }
    partial.content[index].arguments = block.arguments;
    stream.push({ type: "toolcall_end", contentIndex: index, toolCall: block, partial: { ...partial } });
  }
  if (message.stopReason === "pending") {
    throw new Error("Faux response ended without a stop reason");
  }
  if (message.stopReason === "error" || message.stopReason === "aborted") {
    stream.push({ type: "error", reason: message.stopReason, error: message });
    stream.end(message);
    return;
  }
  stream.push({ type: "done", reason: message.stopReason, message });
  stream.end(message);
}
__name(streamWithDeltas, "streamWithDeltas");
function createFauxCore(options) {
  const api = options.api ?? randomId(DEFAULT_API);
  const provider = options.provider ?? DEFAULT_PROVIDER;
  const minTokenSize = Math.max(1, Math.min(options.tokenSize?.min ?? DEFAULT_MIN_TOKEN_SIZE, options.tokenSize?.max ?? DEFAULT_MAX_TOKEN_SIZE));
  const maxTokenSize = Math.max(minTokenSize, options.tokenSize?.max ?? DEFAULT_MAX_TOKEN_SIZE);
  let pendingResponses = [];
  const tokensPerSecond = options.tokensPerSecond;
  const state = { callCount: 0, deferredFetchCount: 0, cancelledDeferred: [] };
  const promptCache = /* @__PURE__ */ new Map();
  const deferredResponses = /* @__PURE__ */ new Map();
  const modelDefinitions = options.models?.length ? options.models : [
    {
      id: DEFAULT_MODEL_ID,
      name: DEFAULT_MODEL_NAME,
      reasoning: false,
      input: ["text", "image"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 128e3,
      maxTokens: 16384
    }
  ];
  const models = modelDefinitions.map((definition) => ({
    id: definition.id,
    name: definition.name ?? definition.id,
    api,
    provider,
    baseUrl: DEFAULT_BASE_URL,
    reasoning: definition.reasoning ?? false,
    input: definition.input ?? ["text", "image"],
    inputLimits: definition.inputLimits,
    cost: definition.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: definition.contextWindow ?? 128e3,
    maxTokens: definition.maxTokens ?? 16384
  }));
  const resolveResponse = /* @__PURE__ */ __name(async (step, context, streamOptions, requestModel) => {
    const resolved = typeof step === "function" ? await step(context, streamOptions, state, requestModel) : step;
    return withUsageEstimate(cloneMessage(resolved, api, provider, requestModel.id), context, streamOptions, promptCache);
  }, "resolveResponse");
  const stream = /* @__PURE__ */ __name((requestModel, context, streamOptions) => {
    const outer = createAssistantMessageEventStream();
    const step = pendingResponses.shift();
    state.callCount++;
    queueMicrotask(async () => {
      try {
        await streamOptions?.onResponse?.({ status: 200, headers: {} }, requestModel);
        if (!step) {
          let message2 = createErrorMessage(new Error("No more faux responses queued"), api, provider, requestModel.id);
          message2 = withUsageEstimate(message2, context, streamOptions, promptCache);
          outer.push({ type: "error", reason: "error", error: message2 });
          outer.end(message2);
          return;
        }
        if (streamOptions?.deferred) {
          const handle = {
            provider: requestModel.provider,
            modelId: requestModel.id,
            api: requestModel.api,
            id: randomId("deferred"),
            ...options.deferred?.pollAfterMs !== void 0 ? { pollAfterMs: options.deferred.pollAfterMs } : {}
          };
          deferredResponses.set(handle.id, {
            handle,
            step,
            context,
            options: streamOptions,
            model: requestModel,
            pendingFetches: Math.max(0, Math.floor(options.deferred?.pendingFetches ?? 0)),
            cancelled: false
          });
          await streamWithDeltas(outer, createDeferredMessage(requestModel, handle), minTokenSize, maxTokenSize, tokensPerSecond, streamOptions.signal);
          return;
        }
        const message = await resolveResponse(step, context, streamOptions, requestModel);
        await streamWithDeltas(outer, message, minTokenSize, maxTokenSize, tokensPerSecond, streamOptions?.signal);
      } catch (error) {
        const message = createErrorMessage(error, api, provider, requestModel.id);
        outer.push({ type: "error", reason: "error", error: message });
        outer.end(message);
      }
    });
    return outer;
  }, "stream");
  const streamSimple = /* @__PURE__ */ __name((streamModel, context, streamOptions) => stream(streamModel, context, streamOptions), "streamSimple");
  const fetchDeferred = /* @__PURE__ */ __name((requestModel, handle, fetchOptions) => {
    const outer = createAssistantMessageEventStream();
    state.deferredFetchCount++;
    queueMicrotask(async () => {
      try {
        await fetchOptions?.onResponse?.({ status: 200, headers: {} }, requestModel);
        const entry = deferredResponses.get(handle.id);
        if (!entry || entry.handle.provider !== handle.provider || entry.handle.modelId !== handle.modelId || entry.handle.api !== handle.api) {
          throw new Error(`Unknown faux deferred response: ${handle.id}`);
        }
        if (entry.cancelled)
          throw new Error(`Faux deferred response was cancelled: ${handle.id}`);
        if (entry.pendingFetches > 0) {
          entry.pendingFetches--;
          await streamWithDeltas(outer, createDeferredMessage(requestModel, entry.handle), minTokenSize, maxTokenSize, tokensPerSecond, fetchOptions?.signal);
          return;
        }
        if (!entry.final) {
          const { deferred: _deferred, signal: _submissionSignal, onResponse: _submissionOnResponse, ...submissionOptions } = entry.options ?? {};
          try {
            entry.final = await resolveResponse(entry.step, entry.context, submissionOptions, entry.model);
          } catch (error) {
            entry.final = createErrorMessage(error, api, provider, entry.model.id);
          }
        }
        await streamWithDeltas(outer, entry.final, minTokenSize, maxTokenSize, tokensPerSecond, fetchOptions?.signal);
      } catch (error) {
        const message = createErrorMessage(error, api, provider, requestModel.id);
        outer.push({ type: "error", reason: "error", error: message });
        outer.end(message);
      }
    });
    return outer;
  }, "fetchDeferred");
  const cancelDeferred = /* @__PURE__ */ __name(async (requestModel, handle, cancelOptions) => {
    state.cancelledDeferred.push(structuredClone(handle));
    const entry = deferredResponses.get(handle.id);
    if (entry)
      entry.cancelled = true;
    await cancelOptions?.onResponse?.({ status: 200, headers: {} }, requestModel);
  }, "cancelDeferred");
  function getModel(requestedModelId) {
    if (!requestedModelId) {
      return models[0];
    }
    return models.find((candidate) => candidate.id === requestedModelId);
  }
  __name(getModel, "getModel");
  return {
    api,
    provider,
    models,
    stream,
    streamSimple,
    fetchDeferred,
    cancelDeferred,
    getModel,
    state,
    setResponses(responses) {
      pendingResponses = [...responses];
    },
    appendResponses(responses) {
      pendingResponses.push(...responses);
    },
    getPendingResponseCount() {
      return pendingResponses.length;
    }
  };
}
__name(createFauxCore, "createFauxCore");
function fauxProvider(options = {}) {
  const core = createFauxCore(options);
  const provider = createProvider({
    id: core.provider,
    auth: { apiKey: { name: "Faux", resolve: /* @__PURE__ */ __name(async () => ({ auth: {} }), "resolve") } },
    models: core.models,
    api: {
      stream: core.stream,
      streamSimple: core.streamSimple,
      fetchDeferred: core.fetchDeferred,
      cancelDeferred: core.cancelDeferred
    }
  });
  return {
    provider,
    api: core.api,
    models: core.models,
    getModel: core.getModel,
    state: core.state,
    setResponses: core.setResponses,
    appendResponses: core.appendResponses,
    getPendingResponseCount: core.getPendingResponseCount
  };
}
__name(fauxProvider, "fauxProvider");

// pi-dist/pi-ai/session-resources.js
var sessionResourceCleanups = /* @__PURE__ */ new Set();
function registerSessionResourceCleanup(cleanup) {
  sessionResourceCleanups.add(cleanup);
  return () => {
    sessionResourceCleanups.delete(cleanup);
  };
}
__name(registerSessionResourceCleanup, "registerSessionResourceCleanup");
function cleanupSessionResources(sessionId) {
  const errors = [];
  for (const cleanup of sessionResourceCleanups) {
    try {
      cleanup(sessionId);
    } catch (error) {
      errors.push(error);
    }
  }
  if (errors.length > 0) {
    throw new AggregateError(errors, "Failed to cleanup session resources");
  }
}
__name(cleanupSessionResources, "cleanupSessionResources");

// pi-dist/pi-ai/utils/json-parse.js
import { parse as partialParse } from "../../../partial-json/dist/index.js";
var VALID_JSON_ESCAPES = /* @__PURE__ */ new Set(['"', "\\", "/", "b", "f", "n", "r", "t", "u"]);
function isControlCharacter(char) {
  const codePoint = char.codePointAt(0);
  return codePoint !== void 0 && codePoint >= 0 && codePoint <= 31;
}
__name(isControlCharacter, "isControlCharacter");
function escapeControlCharacter(char) {
  switch (char) {
    case "\b":
      return "\\b";
    case "\f":
      return "\\f";
    case "\n":
      return "\\n";
    case "\r":
      return "\\r";
    case "	":
      return "\\t";
    default:
      return `\\u${char.codePointAt(0)?.toString(16).padStart(4, "0") ?? "0000"}`;
  }
}
__name(escapeControlCharacter, "escapeControlCharacter");
function repairJson(json) {
  let repaired = "";
  let inString = false;
  for (let index = 0; index < json.length; index++) {
    const char = json[index];
    if (!inString) {
      repaired += char;
      if (char === '"') {
        inString = true;
      }
      continue;
    }
    if (char === '"') {
      repaired += char;
      inString = false;
      continue;
    }
    if (char === "\\") {
      const nextChar = json[index + 1];
      if (nextChar === void 0) {
        repaired += "\\\\";
        continue;
      }
      if (nextChar === "u") {
        const unicodeDigits = json.slice(index + 2, index + 6);
        if (/^[0-9a-fA-F]{4}$/.test(unicodeDigits)) {
          repaired += `\\u${unicodeDigits}`;
          index += 5;
          continue;
        }
      }
      if (VALID_JSON_ESCAPES.has(nextChar)) {
        repaired += `\\${nextChar}`;
        index += 1;
        continue;
      }
      repaired += "\\\\";
      continue;
    }
    repaired += isControlCharacter(char) ? escapeControlCharacter(char) : char;
  }
  return repaired;
}
__name(repairJson, "repairJson");
function parseJsonWithRepair(json) {
  try {
    return JSON.parse(json);
  } catch (error) {
    const repairedJson = repairJson(json);
    if (repairedJson !== json) {
      return JSON.parse(repairedJson);
    }
    throw error;
  }
}
__name(parseJsonWithRepair, "parseJsonWithRepair");
function parseStreamingJson(partialJson) {
  if (!partialJson || partialJson.trim() === "") {
    return {};
  }
  try {
    return parseJsonWithRepair(partialJson);
  } catch {
    try {
      const result = partialParse(partialJson);
      return result ?? {};
    } catch {
      try {
        const result = partialParse(repairJson(partialJson));
        return result ?? {};
      } catch {
        return {};
      }
    }
  }
}
__name(parseStreamingJson, "parseStreamingJson");

// pi-dist/pi-ai/utils/assistant-message-frame.js
function cloneTextContent(content) {
  return {
    type: "text",
    text: content.text,
    ...content.textSignature === void 0 ? {} : { textSignature: content.textSignature }
  };
}
__name(cloneTextContent, "cloneTextContent");
function cloneThinkingContent(content) {
  return {
    type: "thinking",
    thinking: content.thinking,
    ...content.thinkingSignature === void 0 ? {} : { thinkingSignature: content.thinkingSignature },
    ...content.redacted === void 0 ? {} : { redacted: content.redacted }
  };
}
__name(cloneThinkingContent, "cloneThinkingContent");
function cloneToolCall(toolCall) {
  return {
    type: "toolCall",
    id: toolCall.id,
    name: toolCall.name,
    arguments: structuredClone(toolCall.arguments),
    ...toolCall.thoughtSignature === void 0 ? {} : { thoughtSignature: toolCall.thoughtSignature },
    ...toolCall.namespace === void 0 ? {} : { namespace: toolCall.namespace }
  };
}
__name(cloneToolCall, "cloneToolCall");
function cloneStartMessage(message) {
  return {
    role: "assistant",
    content: [],
    api: message.api,
    provider: message.provider,
    model: message.model,
    ...message.responseModel === void 0 ? {} : { responseModel: message.responseModel },
    ...message.responseId === void 0 ? {} : { responseId: message.responseId },
    ...message.providerThinkingLevel === void 0 ? {} : { providerThinkingLevel: message.providerThinkingLevel },
    ...message.diagnostics === void 0 ? {} : { diagnostics: structuredClone(message.diagnostics) },
    usage: structuredClone(message.usage),
    stopReason: "pending",
    timestamp: message.timestamp
  };
}
__name(cloneStartMessage, "cloneStartMessage");
function assertContentIndex(contentIndex) {
  if (!Number.isSafeInteger(contentIndex) || contentIndex < 0) {
    throw new Error(`Invalid assistant message frame contentIndex: ${contentIndex}`);
  }
}
__name(assertContentIndex, "assertContentIndex");
function eventBlock(event) {
  assertContentIndex(event.contentIndex);
  const block = event.partial.content[event.contentIndex];
  if (!block) {
    throw new Error(`${event.type} event has no content block at index ${event.contentIndex}`);
  }
  return block;
}
__name(eventBlock, "eventBlock");
function serializedArguments(argumentsValue) {
  const serialized = JSON.stringify(argumentsValue);
  if (serialized === void 0)
    throw new Error("Tool-call arguments are not JSON-serializable");
  return serialized;
}
__name(serializedArguments, "serializedArguments");
var EMPTY_PARSED_TOOL_ARGUMENTS = serializedArguments(parseStreamingJson(""));
function isJsonPrefix(snapshot, current) {
  if (typeof snapshot === "string")
    return typeof current === "string" && current.startsWith(snapshot);
  if (Array.isArray(snapshot)) {
    return Array.isArray(current) && snapshot.length <= current.length && snapshot.every((value, index) => isJsonPrefix(value, current[index]));
  }
  if (typeof snapshot !== "object" || snapshot === null)
    return Object.is(snapshot, current);
  if (typeof current !== "object" || current === null || Array.isArray(current))
    return false;
  const currentRecord = current;
  return Object.entries(snapshot).every(([key, value]) => Object.hasOwn(currentRecord, key) && isJsonPrefix(value, currentRecord[key]));
}
__name(isJsonPrefix, "isJsonPrefix");
var AssistantMessageFrameEncoder = class {
  static {
    __name(this, "AssistantMessageFrameEncoder");
  }
  started = false;
  terminal = false;
  blocks = /* @__PURE__ */ new Map();
  encode(event) {
    if (this.terminal)
      throw new Error(`Assistant message event ${event.type} follows a terminal event`);
    switch (event.type) {
      case "start":
        if (this.started)
          throw new Error("Assistant message stream contains more than one start event");
        this.started = true;
        return { type: "start", partial: cloneStartMessage(event.partial) };
      case "done":
        if (!this.started)
          throw new Error("Assistant message done event appears before start");
        this.terminal = true;
        return void 0;
      case "error":
        this.terminal = true;
        return void 0;
    }
    if (!this.started)
      throw new Error(`Assistant message ${event.type} event appears before start`);
    switch (event.type) {
      case "text_start": {
        const content = eventBlock(event);
        if (content.type !== "text") {
          throw new Error(`text_start event points to ${content.type} block at index ${event.contentIndex}`);
        }
        this.startBlock(event.contentIndex, {
          kind: "text",
          coveredChars: content.text.length,
          deltaChars: 0
        });
        return { type: "text_start", contentIndex: event.contentIndex, content: cloneTextContent(content) };
      }
      case "text_delta":
        return this.encodeTextDelta(event.contentIndex, event.delta, "text");
      case "text_end": {
        const content = eventBlock(event);
        if (content.type !== "text") {
          throw new Error(`text_end event points to ${content.type} block at index ${event.contentIndex}`);
        }
        this.endBlock(event.contentIndex, "text");
        return {
          type: "text_end",
          contentIndex: event.contentIndex,
          content: event.content,
          ...content.textSignature === void 0 ? {} : { textSignature: content.textSignature }
        };
      }
      case "thinking_start": {
        const content = eventBlock(event);
        if (content.type !== "thinking") {
          throw new Error(`thinking_start event points to ${content.type} block at index ${event.contentIndex}`);
        }
        this.startBlock(event.contentIndex, {
          kind: "thinking",
          coveredChars: content.thinking.length,
          deltaChars: 0
        });
        return {
          type: "thinking_start",
          contentIndex: event.contentIndex,
          content: cloneThinkingContent(content)
        };
      }
      case "thinking_delta":
        return this.encodeTextDelta(event.contentIndex, event.delta, "thinking");
      case "thinking_end": {
        const content = eventBlock(event);
        if (content.type !== "thinking") {
          throw new Error(`thinking_end event points to ${content.type} block at index ${event.contentIndex}`);
        }
        this.endBlock(event.contentIndex, "thinking");
        return {
          type: "thinking_end",
          contentIndex: event.contentIndex,
          content: event.content,
          ...content.thinkingSignature === void 0 ? {} : { thinkingSignature: content.thinkingSignature },
          ...content.redacted === void 0 ? {} : { redacted: content.redacted }
        };
      }
      case "toolcall_start": {
        const content = eventBlock(event);
        if (content.type !== "toolCall") {
          throw new Error(`toolcall_start event points to ${content.type} block at index ${event.contentIndex}`);
        }
        const snapshotArguments = serializedArguments(content.arguments);
        const caughtUp = snapshotArguments === EMPTY_PARSED_TOOL_ARGUMENTS;
        this.startBlock(event.contentIndex, {
          kind: "toolCall",
          caughtUp,
          catchupJson: "",
          snapshotArguments: caughtUp ? "" : snapshotArguments
        });
        return { type: "toolcall_start", contentIndex: event.contentIndex, toolCall: cloneToolCall(content) };
      }
      case "toolcall_delta": {
        const state = this.block(event.contentIndex, "toolCall");
        if (state.kind !== "toolCall")
          throw new Error("Unreachable tool-call encoder state");
        if (state.caughtUp) {
          return event.delta.length === 0 ? void 0 : { type: "toolcall_delta", contentIndex: event.contentIndex, delta: event.delta };
        }
        state.catchupJson += event.delta;
        const argumentsValue = parseStreamingJson(state.catchupJson);
        if (serializedArguments(argumentsValue) !== state.snapshotArguments) {
          const snapshotArguments = parseStreamingJson(state.snapshotArguments);
          if (!isJsonPrefix(snapshotArguments, argumentsValue))
            return void 0;
        }
        state.caughtUp = true;
        state.snapshotArguments = "";
        const json = state.catchupJson;
        state.catchupJson = "";
        return json.length === 0 ? void 0 : { type: "toolcall_checkpoint", contentIndex: event.contentIndex, json };
      }
      case "toolcall_end": {
        const content = eventBlock(event);
        if (content.type !== "toolCall") {
          throw new Error(`toolcall_end event points to ${content.type} block at index ${event.contentIndex}`);
        }
        if (event.toolCall.type !== "toolCall") {
          throw new Error(`toolcall_end event has invalid tool call at index ${event.contentIndex}`);
        }
        this.endBlock(event.contentIndex, "toolCall");
        return {
          type: "toolcall_end",
          contentIndex: event.contentIndex,
          id: event.toolCall.id,
          name: event.toolCall.name,
          arguments: structuredClone(event.toolCall.arguments),
          ...event.toolCall.thoughtSignature === void 0 ? {} : { thoughtSignature: event.toolCall.thoughtSignature },
          ...event.toolCall.namespace === void 0 ? {} : { namespace: event.toolCall.namespace }
        };
      }
    }
  }
  startBlock(contentIndex, state) {
    assertContentIndex(contentIndex);
    if (this.blocks.has(contentIndex)) {
      throw new Error(`Assistant message block ${contentIndex} starts more than once`);
    }
    this.blocks.set(contentIndex, state);
  }
  block(contentIndex, kind) {
    assertContentIndex(contentIndex);
    const state = this.blocks.get(contentIndex);
    if (state === void 0)
      throw new Error(`Assistant message ${kind} block ${contentIndex} has not started`);
    if (state.kind !== kind) {
      throw new Error(`Assistant message block ${contentIndex} is ${state.kind}, not ${kind}`);
    }
    return state;
  }
  endBlock(contentIndex, kind) {
    this.block(contentIndex, kind);
    this.blocks.delete(contentIndex);
  }
  encodeTextDelta(contentIndex, delta, kind) {
    const state = this.block(contentIndex, kind);
    if (state.kind === "toolCall")
      throw new Error("Unreachable text encoder state");
    const deltaStart = state.deltaChars;
    state.deltaChars += delta.length;
    const covered = Math.max(0, state.coveredChars - deltaStart);
    if (covered >= delta.length)
      return void 0;
    const uncovered = covered === 0 ? delta : delta.slice(covered);
    return kind === "text" ? { type: "text_delta", contentIndex, delta: uncovered } : { type: "thinking_delta", contentIndex, delta: uncovered };
  }
};
function appendBlock(message, states, contentIndex, block, state) {
  assertContentIndex(contentIndex);
  if (contentIndex !== message.content.length) {
    const reason = contentIndex < message.content.length ? "already exists" : "would leave a gap";
    throw new Error(`Cannot start assistant message block at index ${contentIndex}: ${reason}`);
  }
  message.content.push(structuredClone(block));
  states.set(contentIndex, state);
}
__name(appendBlock, "appendBlock");
function activeBlock(message, states, contentIndex, expectedKind, frameType) {
  assertContentIndex(contentIndex);
  const state = states.get(contentIndex);
  const block = message.content[contentIndex];
  if (!state || !block) {
    throw new Error(`${frameType} frame has no started block at index ${contentIndex}`);
  }
  if (state.kind !== expectedKind || block.type !== expectedKind) {
    throw new Error(`${frameType} frame expected ${expectedKind} block at index ${contentIndex}, found ${block.type}`);
  }
  if (state.ended) {
    throw new Error(`${frameType} frame follows the end of block at index ${contentIndex}`);
  }
  return { block, state };
}
__name(activeBlock, "activeBlock");
function reduceAssistantMessageFrames(frames) {
  let message;
  let frameBeforeStart;
  const states = /* @__PURE__ */ new Map();
  for (const frame of frames) {
    if (frame.type === "start") {
      if (message)
        throw new Error("Assistant message frame sequence contains more than one start frame");
      if (frameBeforeStart !== void 0)
        throw new Error(`${frameBeforeStart} frame appears before the start frame`);
      message = structuredClone(frame.partial);
      continue;
    }
    if (!message) {
      frameBeforeStart ??= frame.type;
      continue;
    }
    switch (frame.type) {
      case "text_start":
        if (frame.content.type !== "text") {
          throw new Error(`text_start frame contains ${frame.content.type} content`);
        }
        appendBlock(message, states, frame.contentIndex, frame.content, { kind: "text", ended: false });
        break;
      case "text_delta": {
        const { block } = activeBlock(message, states, frame.contentIndex, "text", frame.type);
        if (block.type !== "text")
          throw new Error("Unreachable text frame state");
        block.text += frame.delta;
        break;
      }
      case "text_end": {
        const { block, state } = activeBlock(message, states, frame.contentIndex, "text", frame.type);
        if (block.type !== "text")
          throw new Error("Unreachable text frame state");
        block.text = frame.content;
        delete block.textSignature;
        if (frame.textSignature !== void 0)
          block.textSignature = frame.textSignature;
        state.ended = true;
        break;
      }
      case "thinking_start":
        if (frame.content.type !== "thinking") {
          throw new Error(`thinking_start frame contains ${frame.content.type} content`);
        }
        appendBlock(message, states, frame.contentIndex, frame.content, {
          kind: "thinking",
          ended: false
        });
        break;
      case "thinking_delta": {
        const { block } = activeBlock(message, states, frame.contentIndex, "thinking", frame.type);
        if (block.type !== "thinking")
          throw new Error("Unreachable thinking frame state");
        block.thinking += frame.delta;
        break;
      }
      case "thinking_end": {
        const { block, state } = activeBlock(message, states, frame.contentIndex, "thinking", frame.type);
        if (block.type !== "thinking")
          throw new Error("Unreachable thinking frame state");
        block.thinking = frame.content;
        delete block.thinkingSignature;
        delete block.redacted;
        if (frame.thinkingSignature !== void 0)
          block.thinkingSignature = frame.thinkingSignature;
        if (frame.redacted !== void 0)
          block.redacted = frame.redacted;
        state.ended = true;
        break;
      }
      case "toolcall_start":
        if (frame.toolCall.type !== "toolCall") {
          throw new Error(`toolcall_start frame contains ${frame.toolCall.type} content`);
        }
        appendBlock(message, states, frame.contentIndex, frame.toolCall, {
          kind: "toolCall",
          ended: false,
          json: ""
        });
        break;
      case "toolcall_checkpoint": {
        const { block, state } = activeBlock(message, states, frame.contentIndex, "toolCall", frame.type);
        if (block.type !== "toolCall" || state.kind !== "toolCall") {
          throw new Error("Unreachable tool-call checkpoint state");
        }
        state.json = frame.json;
        block.arguments = parseStreamingJson(frame.json);
        break;
      }
      case "toolcall_delta": {
        const { block, state } = activeBlock(message, states, frame.contentIndex, "toolCall", frame.type);
        if (block.type !== "toolCall" || state.kind !== "toolCall") {
          throw new Error("Unreachable tool-call frame state");
        }
        state.json += frame.delta;
        break;
      }
      case "toolcall_end": {
        const { block, state } = activeBlock(message, states, frame.contentIndex, "toolCall", frame.type);
        if (block.type !== "toolCall")
          throw new Error("Unreachable tool-call frame state");
        block.id = frame.id;
        block.name = frame.name;
        block.arguments = structuredClone(frame.arguments);
        delete block.thoughtSignature;
        delete block.namespace;
        if (frame.thoughtSignature !== void 0)
          block.thoughtSignature = frame.thoughtSignature;
        if (frame.namespace !== void 0)
          block.namespace = frame.namespace;
        state.ended = true;
        break;
      }
    }
  }
  if (!message)
    return void 0;
  for (const [contentIndex, state] of states) {
    if (state.kind !== "toolCall" || state.ended || state.json.length === 0)
      continue;
    const block = message.content[contentIndex];
    if (block?.type !== "toolCall")
      throw new Error("Unreachable tool-call frame state");
    block.arguments = parseStreamingJson(state.json);
  }
  return message;
}
__name(reduceAssistantMessageFrames, "reduceAssistantMessageFrames");

// pi-dist/pi-ai/index.js
import { createAssistantMessageEventStream as createAssistantMessageEventStream2, EventStream, AssistantMessageEventStream } from "../utils/event-stream.js";

// pi-dist/pi-ai/utils/overflow.js
var OVERFLOW_PATTERNS = [
  /prompt (?:is )?too long/i,
  // Anthropic and z.ai token overflow
  /prompt exceeds max length/i,
  // z.ai CN endpoint token overflow
  /request_too_large/i,
  // Anthropic request byte-size overflow (HTTP 413)
  /input is too long for requested model/i,
  // Amazon Bedrock
  /exceeds the context window/i,
  // OpenAI (Completions & Responses API)
  /exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))/i,
  // OpenAI-compatible proxies (LiteLLM)
  /input token count.*exceeds the maximum/i,
  // Google (Gemini)
  /maximum prompt length is \d+/i,
  // xAI (Grok)
  /reduce the length of the messages/i,
  // Groq
  /maximum context length is \d+ tokens/i,
  // OpenRouter (most backends)
  /exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?/i,
  // OpenRouter/Poolside
  /input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)/i,
  // Together AI
  /exceeds the limit of \d+/i,
  // GitHub Copilot
  /exceeds the available context size/i,
  // llama.cpp server
  /greater than the context length/i,
  // LM Studio
  /context window exceeds limit/i,
  // MiniMax
  /exceeded model token limit/i,
  // Kimi For Coding
  /too large for model with \d+ maximum context length/i,
  // Mistral
  /prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?/i,
  // DS4 server
  /model_context_window_exceeded/i,
  // z.ai non-standard finish_reason surfaced as error text
  /prompt too long; exceeded (?:max )?context length/i,
  // Ollama explicit overflow error
  /range of input length should be/i,
  // DashScope / Qwen Token Plan
  /context[_ ]length[_ ]exceeded/i,
  // Generic fallback
  /too many tokens/i,
  // Generic fallback
  /token limit exceeded/i
  // Generic fallback
];
var CEREBRAS_BODYLESS_OVERFLOW_PATTERN = /^4(?:00|13)\s*(?:status code)?\s*\(no body\)/i;
var NON_OVERFLOW_PATTERNS = [
  /^(Throttling error|Service unavailable):/i,
  // AWS Bedrock non-overflow errors (human-readable prefixes from formatBedrockError)
  /rate limit/i,
  // Generic rate limiting
  /too many requests/i
  // Generic HTTP 429 style
];
function isContextOverflow(message, contextWindow) {
  if (message.stopReason === "error" && message.errorMessage) {
    const isNonOverflow = NON_OVERFLOW_PATTERNS.some((p) => p.test(message.errorMessage));
    if (!isNonOverflow) {
      if (OVERFLOW_PATTERNS.some((p) => p.test(message.errorMessage))) {
        return true;
      }
      if (message.provider === "cerebras" && CEREBRAS_BODYLESS_OVERFLOW_PATTERN.test(message.errorMessage)) {
        return true;
      }
    }
  }
  if (contextWindow && message.stopReason === "stop") {
    const inputTokens = message.usage.input + message.usage.cacheRead;
    if (inputTokens > contextWindow) {
      return true;
    }
  }
  if (contextWindow && message.stopReason === "length" && message.usage.output === 0) {
    const inputTokens = message.usage.input + message.usage.cacheRead;
    if (inputTokens >= contextWindow * 0.99) {
      return true;
    }
  }
  return false;
}
__name(isContextOverflow, "isContextOverflow");
function isRecoverableLength(message, desiredMaxOutput) {
  return message.stopReason === "length" && desiredMaxOutput > 0 && message.usage.output < desiredMaxOutput;
}
__name(isRecoverableLength, "isRecoverableLength");
function getOverflowPatterns() {
  return [...OVERFLOW_PATTERNS];
}
__name(getOverflowPatterns, "getOverflowPatterns");

// pi-dist/pi-ai/utils/retry.js
function buildProviderErrorPattern(patterns) {
  return new RegExp(patterns.join("|"), "i");
}
__name(buildProviderErrorPattern, "buildProviderErrorPattern");
var NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN = buildProviderErrorPattern([
  // OpenCode Go/free-tier limits returned as 429 JSON error types by OpenCode's
  // Zen API. These are subscription/account limits, not transient throttles.
  "GoUsageLimitError",
  "FreeUsageLimitError",
  // OpenCode Go subscription-limit text asks users to enable available-balance
  // usage after rolling/weekly/monthly limits are reached.
  "Monthly usage limit reached",
  "available balance",
  // Generic quota/budget/billing exhaustion. `insufficient_quota` is OpenAI's
  // quota/billing error code; the other strings cover common gateway wording.
  "insufficient_quota",
  "out of budget",
  "quota exceeded",
  "billing",
  // Sign in with ChatGPT: the subscription's shared usage limit, which resets
  // after hours rather than seconds.
  "subscription_sharing_usage_limit_exceeded"
]);
var RETRYABLE_PROVIDER_ERROR_PATTERN = buildProviderErrorPattern([
  // Generic provider load, HTTP status, and server-side transient failures.
  "overloaded",
  "currently experiencing high demand",
  "rate.?limit",
  "too many requests",
  "429",
  "500",
  "502",
  "503",
  "504",
  "520",
  "524",
  "service.?unavailable",
  "server.?error",
  "internal.?error",
  // Wrapper/provider text for transient upstream failures, including OpenRouter
  // "Provider returned error" responses (#2264).
  "provider.?returned.?error",
  "exceeded request buffer limit while retrying upstream",
  // Network, proxy, and fetch transport failures. This includes OpenAI Codex
  // raw-fetch failures such as "upstream connect", "connection refused", and
  // "reset before headers" (#733), plus OpenRouter connection drops (#3317).
  "network.?error",
  "connection.?error",
  "connection.?refused",
  "connection.?lost",
  "other side closed",
  "fetch failed",
  "getaddrinfo",
  "ENOTFOUND",
  "EAI_AGAIN",
  "upstream.?connect",
  "reset before headers",
  "socket hang up",
  "socket connection was closed",
  "timed? out",
  "timeout",
  "terminated",
  // WebSocket transports can report close/error text instead of HTTP/fetch text.
  "websocket.?closed",
  "websocket.?error",
  // Premature stream endings from SDKs and transports. Anthropic can throw
  // "stream ended without ..." and "Anthropic stream ended before message_stop"
  // (#4433); Bedrock/Smithy can throw an HTTP/2 no-response error (#3594).
  "ended without",
  "stream ended before message_stop",
  "stream ended before a terminal response event",
  "http2 request did not get a response",
  // Provider-requested retry delay cap failures should flow through the outer
  // retry policy so callers can surface/abort the backoff (#1123).
  "retry delay",
  // Explicit retry guidance emitted mid-stream by OpenAI Responses and Bedrock
  // stream exceptions (#6019).
  "you can retry your request",
  "try your request again",
  "please retry your request",
  // gRPC based providers (e.g. NVIDIA NIM)
  "ResourceExhausted",
  // Sign in with ChatGPT: usage or user data temporarily unavailable. Usage
  // failures can arrive mid-stream without an HTTP 503 in the message.
  "subscription_sharing_usage_unavailable",
  "subscription_sharing_user_unavailable"
]);
var DEFAULT_MAX_AGENT_RETRY_DELAY_MS = 6e4;
function retryDelayMs(policy, attempt) {
  const delay = policy.baseDelayMs * 2 ** Math.max(0, attempt - 1);
  const safeDelay = Number.isSafeInteger(delay) ? delay : Number.MAX_SAFE_INTEGER;
  return Math.min(safeDelay, policy.maxAgentDelayMs ?? DEFAULT_MAX_AGENT_RETRY_DELAY_MS);
}
__name(retryDelayMs, "retryDelayMs");
var RetrySleepAbortError = class extends Error {
  static {
    __name(this, "RetrySleepAbortError");
  }
  constructor() {
    super("Aborted");
  }
};
function sleep(ms, signal) {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new RetrySleepAbortError());
      return;
    }
    const timeout = setTimeout(resolve, ms);
    signal?.addEventListener("abort", () => {
      clearTimeout(timeout);
      reject(new RetrySleepAbortError());
    }, { once: true });
  });
}
__name(sleep, "sleep");
async function retryAssistantCall(produce, policy, signal, callbacks) {
  const maxAttempts = policy?.enabled ? policy.maxRetries : 0;
  let attempt = 0;
  let lastRetry;
  for (; ; ) {
    const response = await produce();
    if (response.stopReason === "aborted") {
      if (lastRetry)
        await callbacks?.onRetryFinished?.(false, lastRetry.attempt);
      return response;
    }
    if (response.stopReason !== "error") {
      if (lastRetry)
        await callbacks?.onRetryFinished?.(true, lastRetry.attempt);
      return response;
    }
    if (attempt >= maxAttempts || !isRetryableAssistantError(response)) {
      if (lastRetry)
        await callbacks?.onRetryFinished?.(false, lastRetry.attempt, response.errorMessage);
      return response;
    }
    attempt++;
    lastRetry = { attempt, errorMessage: response.errorMessage || "Unknown error" };
    const delayMs = retryDelayMs(policy, attempt);
    await callbacks?.onRetryScheduled?.(attempt, maxAttempts, delayMs, lastRetry.errorMessage);
    try {
      await sleep(delayMs, signal);
    } catch (error) {
      await callbacks?.onRetryFinished?.(false, attempt, lastRetry.errorMessage);
      if (error instanceof RetrySleepAbortError) {
        const { errorMessage: _errorMessage, ...rest } = response;
        return { ...rest, stopReason: "aborted" };
      }
      throw error;
    }
    await callbacks?.onRetryAttemptStart?.();
  }
}
__name(retryAssistantCall, "retryAssistantCall");
function isRetryableAssistantError(message) {
  if (message.stopReason !== "error" || !message.errorMessage)
    return false;
  const errorMessage = message.errorMessage;
  if (NON_RETRYABLE_PROVIDER_LIMIT_ERROR_PATTERN.test(errorMessage))
    return false;
  return RETRYABLE_PROVIDER_ERROR_PATTERN.test(errorMessage);
}
__name(isRetryableAssistantError, "isRetryableAssistantError");

// pi-dist/pi-ai/utils/typebox-helpers.js
import { Type } from "../../../typebox.mjs";
function StringEnum(values, options) {
  return Type.Unsafe({
    type: "string",
    enum: values,
    ...options?.description && { description: options.description },
    ...options?.default && { default: options.default }
  });
}
__name(StringEnum, "StringEnum");

// pi-dist/pi-ai/index.js
import { uuidv7 } from "../utils/uuid.js";

// pi-dist/pi-ai/utils/validation.js
import { Compile } from "../../../typebox-compile.mjs";
import { Value } from "../../../typebox-value.mjs";
var validatorCache = /* @__PURE__ */ new WeakMap();
var TYPEBOX_KIND = /* @__PURE__ */ Symbol.for("TypeBox.Kind");
function getSchemaTypes(schema) {
  if (typeof schema.type === "string") {
    return [schema.type];
  }
  if (Array.isArray(schema.type)) {
    return schema.type.filter((type) => typeof type === "string");
  }
  return [];
}
__name(getSchemaTypes, "getSchemaTypes");
function matchesJsonType(value, type) {
  switch (type) {
    case "number":
      return typeof value === "number";
    case "integer":
      return typeof value === "number" && Number.isInteger(value);
    case "boolean":
      return typeof value === "boolean";
    case "string":
      return typeof value === "string";
    case "null":
      return value === null;
    case "array":
      return Array.isArray(value);
    case "object":
      return typeof value === "object" && value !== null && !Array.isArray(value);
    default:
      return false;
  }
}
__name(matchesJsonType, "matchesJsonType");
function getSubSchemaValidator(schema) {
  try {
    return getValidator(schema);
  } catch {
    return void 0;
  }
}
__name(getSubSchemaValidator, "getSubSchemaValidator");
function coercePrimitiveByType(value, type) {
  switch (type) {
    case "number": {
      if (value === null) {
        return 0;
      }
      if (typeof value === "string" && value.trim() !== "") {
        const parsed = Number(value);
        if (Number.isFinite(parsed)) {
          return parsed;
        }
      }
      if (typeof value === "boolean") {
        return value ? 1 : 0;
      }
      return value;
    }
    case "integer": {
      if (value === null) {
        return 0;
      }
      if (typeof value === "string" && value.trim() !== "") {
        const parsed = Number(value);
        if (Number.isInteger(parsed)) {
          return parsed;
        }
      }
      if (typeof value === "boolean") {
        return value ? 1 : 0;
      }
      return value;
    }
    case "boolean": {
      if (value === null) {
        return false;
      }
      if (typeof value === "string") {
        if (value === "true") {
          return true;
        }
        if (value === "false") {
          return false;
        }
      }
      if (typeof value === "number") {
        if (value === 1) {
          return true;
        }
        if (value === 0) {
          return false;
        }
      }
      return value;
    }
    case "string": {
      if (value === null) {
        return "";
      }
      if (typeof value === "number" || typeof value === "boolean") {
        return String(value);
      }
      return value;
    }
    case "null": {
      if (value === "" || value === 0 || value === false) {
        return null;
      }
      return value;
    }
    default:
      return value;
  }
}
__name(coercePrimitiveByType, "coercePrimitiveByType");
function applySchemaObjectCoercion(value, schema) {
  const properties = schema.properties;
  const definedKeys = new Set(properties ? Object.keys(properties) : []);
  if (properties) {
    for (const [key, propertySchema] of Object.entries(properties)) {
      if (!(key in value)) {
        continue;
      }
      value[key] = coerceWithJsonSchema(value[key], propertySchema);
    }
  }
  if (schema.additionalProperties && typeof schema.additionalProperties === "object") {
    for (const [key, propertyValue] of Object.entries(value)) {
      if (definedKeys.has(key)) {
        continue;
      }
      value[key] = coerceWithJsonSchema(propertyValue, schema.additionalProperties);
    }
  }
}
__name(applySchemaObjectCoercion, "applySchemaObjectCoercion");
function applySchemaArrayCoercion(value, schema) {
  if (Array.isArray(schema.items)) {
    for (let index = 0; index < value.length; index++) {
      const itemSchema = schema.items[index];
      if (!itemSchema) {
        continue;
      }
      value[index] = coerceWithJsonSchema(value[index], itemSchema);
    }
    return;
  }
  if (schema.items && typeof schema.items === "object") {
    for (let index = 0; index < value.length; index++) {
      value[index] = coerceWithJsonSchema(value[index], schema.items);
    }
  }
}
__name(applySchemaArrayCoercion, "applySchemaArrayCoercion");
function coerceWithUnionSchema(value, schemas) {
  for (const schema of schemas) {
    const validator = getSubSchemaValidator(schema);
    if (validator?.Check(value)) {
      return value;
    }
  }
  for (const schema of schemas) {
    const candidate = structuredClone(value);
    const coerced = coerceWithJsonSchema(candidate, schema);
    const validator = getSubSchemaValidator(schema);
    if (validator?.Check(coerced)) {
      return coerced;
    }
  }
  return value;
}
__name(coerceWithUnionSchema, "coerceWithUnionSchema");
function coerceWithJsonSchema(value, schema) {
  let nextValue = value;
  if (Array.isArray(schema.allOf)) {
    for (const nested of schema.allOf) {
      nextValue = coerceWithJsonSchema(nextValue, nested);
    }
  }
  if (Array.isArray(schema.anyOf)) {
    nextValue = coerceWithUnionSchema(nextValue, schema.anyOf);
  }
  if (Array.isArray(schema.oneOf)) {
    nextValue = coerceWithUnionSchema(nextValue, schema.oneOf);
  }
  const schemaTypes = getSchemaTypes(schema);
  const matchesUnionMember = schemaTypes.length > 1 && schemaTypes.some((schemaType) => matchesJsonType(nextValue, schemaType));
  if (schemaTypes.length > 0 && !matchesUnionMember) {
    for (const schemaType of schemaTypes) {
      const candidate = coercePrimitiveByType(nextValue, schemaType);
      if (candidate !== nextValue) {
        nextValue = candidate;
        break;
      }
    }
  }
  if (schemaTypes.includes("object") && typeof nextValue === "object" && nextValue !== null && !Array.isArray(nextValue)) {
    applySchemaObjectCoercion(nextValue, schema);
  }
  if (schemaTypes.includes("array") && Array.isArray(nextValue)) {
    applySchemaArrayCoercion(nextValue, schema);
  }
  return nextValue;
}
__name(coerceWithJsonSchema, "coerceWithJsonSchema");
function normalizeOptionalNulls(value, schema) {
  if (Array.isArray(value)) {
    if (Array.isArray(schema.items)) {
      for (let index = 0; index < value.length; index++) {
        const itemSchema = schema.items[index];
        if (itemSchema)
          normalizeOptionalNulls(value[index], itemSchema);
      }
    } else if (schema.items) {
      for (const item of value)
        normalizeOptionalNulls(item, schema.items);
    }
    return;
  }
  if (typeof value !== "object" || value === null || !schema.properties)
    return;
  const object = value;
  const required = new Set(schema.required ?? []);
  for (const [key, propertySchema] of Object.entries(schema.properties)) {
    if (!(key in object))
      continue;
    if (object[key] === null && !required.has(key) && typeof propertySchema.$ref !== "string" && getSubSchemaValidator(propertySchema)?.Check(null) === false) {
      delete object[key];
    } else {
      normalizeOptionalNulls(object[key], propertySchema);
    }
  }
}
__name(normalizeOptionalNulls, "normalizeOptionalNulls");
function getValidator(schema) {
  const key = schema;
  const cached = validatorCache.get(key);
  if (cached) {
    return cached;
  }
  const validator = Compile(schema);
  validatorCache.set(key, validator);
  return validator;
}
__name(getValidator, "getValidator");
function formatValidationPath(error) {
  if (error.keyword === "required") {
    const requiredProperties = error.params.requiredProperties;
    const requiredProperty = requiredProperties?.[0];
    if (requiredProperty) {
      const basePath = error.instancePath.replace(/^\//, "").replace(/\//g, ".");
      return basePath ? `${basePath}.${requiredProperty}` : requiredProperty;
    }
  }
  const path = error.instancePath.replace(/^\//, "").replace(/\//g, ".");
  return path || "root";
}
__name(formatValidationPath, "formatValidationPath");
function validateToolCall(tools, toolCall) {
  const tool = tools.find((t) => t.name === toolCall.name);
  if (!tool) {
    throw new Error(`Tool "${toolCall.name}" not found`);
  }
  return validateToolArguments(tool, toolCall);
}
__name(validateToolCall, "validateToolCall");
function validateToolArguments(tool, toolCall) {
  const args = structuredClone(toolCall.arguments);
  normalizeOptionalNulls(args, tool.parameters);
  Value.Convert(tool.parameters, args);
  const validator = getValidator(tool.parameters);
  if (!Object.getOwnPropertySymbols(tool.parameters).includes(TYPEBOX_KIND)) {
    const coerced = coerceWithJsonSchema(args, tool.parameters);
    if (coerced !== args) {
      if (typeof args === "object" && args !== null && typeof coerced === "object" && coerced !== null) {
        for (const key of Object.keys(args)) {
          delete args[key];
        }
        Object.assign(args, coerced);
      } else {
        return validator.Check(coerced) ? coerced : args;
      }
    }
  }
  if (validator.Check(args)) {
    return args;
  }
  const errors = validator.Errors(args).map((error) => `  - ${formatValidationPath(error)}: ${error.message}`).join("\n") || "Unknown validation error";
  const errorMessage = `Validation failed for tool "${toolCall.name}":
${errors}

Received arguments:
${JSON.stringify(toolCall.arguments, null, 2)}`;
  throw new Error(errorMessage);
}
__name(validateToolArguments, "validateToolArguments");

export {
  fauxText,
  fauxThinking,
  fauxToolCall,
  fauxAssistantMessage,
  createFauxCore,
  fauxProvider,
  registerSessionResourceCleanup,
  cleanupSessionResources,
  repairJson,
  parseJsonWithRepair,
  parseStreamingJson,
  AssistantMessageFrameEncoder,
  reduceAssistantMessageFrames,
  isContextOverflow,
  isRecoverableLength,
  getOverflowPatterns,
  DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
  retryDelayMs,
  retryAssistantCall,
  isRetryableAssistantError,
  StringEnum,
  validateToolCall,
  validateToolArguments,
  Type2 as Type,
  lazyStream,
  lazyApi,
  createAssistantMessageEventStream2 as createAssistantMessageEventStream,
  EventStream,
  AssistantMessageEventStream,
  uuidv7
};
