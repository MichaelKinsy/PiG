import { getRuntime } from "../state.mjs";
import { Type } from "./typebox.mjs";

export { Type };

let lastTimestamp = -Infinity;
let sequence = 0;

export function uuidv7() {
  const random = new Uint8Array(16);
  if (globalThis.crypto?.getRandomValues) globalThis.crypto.getRandomValues(random);
  else for (let index = 0; index < random.length; index++) random[index] = Math.floor(Math.random() * 256);
  const timestamp = Date.now();
  if (timestamp > lastTimestamp) {
    sequence = random[6] * 0x1000000 + random[7] * 0x10000 + random[8] * 0x100 + random[9];
    lastTimestamp = timestamp;
  } else {
    sequence = (sequence + 1) >>> 0;
    if (sequence === 0) lastTimestamp++;
  }
  const bytes = new Uint8Array(16);
  bytes[0] = (lastTimestamp / 0x10000000000) & 0xff;
  bytes[1] = (lastTimestamp / 0x100000000) & 0xff;
  bytes[2] = (lastTimestamp / 0x1000000) & 0xff;
  bytes[3] = (lastTimestamp / 0x10000) & 0xff;
  bytes[4] = (lastTimestamp / 0x100) & 0xff;
  bytes[5] = lastTimestamp & 0xff;
  bytes[6] = 0x70 | ((sequence >>> 28) & 0x0f);
  bytes[7] = (sequence >>> 20) & 0xff;
  bytes[8] = 0x80 | ((sequence >>> 14) & 0x3f);
  bytes[9] = (sequence >>> 6) & 0xff;
  bytes[10] = ((sequence & 0x3f) << 2) | (random[10] & 0x03);
  bytes.set(random.subarray(11), 11);
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0"));
  return `${hex.slice(0, 4).join("")}-${hex.slice(4, 6).join("")}-${hex.slice(6, 8).join("")}-${hex.slice(8, 10).join("")}-${hex.slice(10, 16).join("")}`;
}

export function StringEnum(values, options = {}) {
  return { type: "string", enum: [...values], ...options };
}

export function contentText(content, separator = "\n") {
  if (typeof content === "string") return content;
  return (content ?? []).filter((block) => block.type === "text").map((block) => block.text).join(separator);
}

export function stream(model, context = {}, options = {}) {
  const runtime = getRuntime();
  if (!runtime) throw new Error("pi-ai.stream: runtime not initialized");
  return runtime.startModelStream(model, context, options);
}

export function streamSimple(model, context = {}, options = {}) {
  const runtime = getRuntime();
  if (!runtime) throw new Error("pi-ai.streamSimple: runtime not initialized");
  return runtime.startModelStream(model, context, options);
}

export async function complete(model, context = {}, options = {}) {
  return stream(model, context, options).result();
}

export class UserMessage {
  constructor(content) {
    this.role = "user";
    this.content = content;
  }
}

export class AssistantMessage {
  constructor(content) {
    this.role = "assistant";
    this.content = content;
  }
}

export class Message {
  constructor(role, content) {
    this.role = role;
    this.content = content;
  }
}

// utils/text.ts (Pi 0.87.1)
function systemContentText(content) {
  if (typeof content === "string") return content;
  return (content ?? []).filter((block) => block.type === "text").map((block) => block.text).join("\n");
}

export function getSystemMessageText(message) {
  const parts = [systemContentText(message.content)];
  for (const text of Object.values(message.sections ?? {})) {
    if (text !== null) parts.push(text);
  }
  return parts.filter((part) => part.length > 0).join("\n\n");
}

export function renderSystemMessageUpdate(message) {
  const parts = [];
  const text = systemContentText(message.content);
  if (text.length > 0) parts.push(text);
  for (const [name, value] of Object.entries(message.sections ?? {})) {
    parts.push(value === null ? `Removed system prompt section "${name}".` : `Updated system prompt section "${name}":\n\n${value}`);
  }
  return parts.join("\n\n");
}

// pig divergence (D73): these pi-ai values (model/provider construction,
// stream internals, faux provider, transcript helpers) run in PiG's Go host.
// They are importable so no extension fails at link time, and throw a
// descriptive error only when called or constructed.
function hostOnlyAi(name) {
  return new Error(`${name} is not available to extensions running in PiG: pi-ai's model and provider layer runs in PiG's host (see DIVERGENCES.md D73)`);
}
function hostOnlyAiFunction(name) {
  const fn = function () { throw hostOnlyAi(name); };
  Object.defineProperty(fn, "name", { value: name });
  return fn;
}
function hostOnlyAiClass(name) {
  return { [name]: class { constructor() { throw hostOnlyAi(name); } } }[name];
}

// utils/retry.ts
export const DEFAULT_MAX_AGENT_RETRY_DELAY_MS = 60_000;
export const AssistantMessageEventStream = hostOnlyAiClass("AssistantMessageEventStream");
export const AssistantMessageFrameEncoder = hostOnlyAiClass("AssistantMessageFrameEncoder");
export const EventStream = hostOnlyAiClass("EventStream");
export const InMemoryCredentialStore = hostOnlyAiClass("InMemoryCredentialStore");
export const InMemoryModelsStore = hostOnlyAiClass("InMemoryModelsStore");
export const ModelsError = hostOnlyAiClass("ModelsError");
export const appendAssistantMessageDiagnostic = hostOnlyAiFunction("appendAssistantMessageDiagnostic");
export const calculateCost = hostOnlyAiFunction("calculateCost");
export const clampThinkingLevel = hostOnlyAiFunction("clampThinkingLevel");
export const cleanupSessionResources = hostOnlyAiFunction("cleanupSessionResources");
export const collapseSystemMessages = hostOnlyAiFunction("collapseSystemMessages");
export const createAssistantMessageDiagnostic = hostOnlyAiFunction("createAssistantMessageDiagnostic");
export const createAssistantMessageEventStream = hostOnlyAiFunction("createAssistantMessageEventStream");
export const createFauxCore = hostOnlyAiFunction("createFauxCore");
export const createImagesModels = hostOnlyAiFunction("createImagesModels");
export const createImagesProvider = hostOnlyAiFunction("createImagesProvider");
export const createInitialSystemMessage = hostOnlyAiFunction("createInitialSystemMessage");
export const createModels = hostOnlyAiFunction("createModels");
export const createProvider = hostOnlyAiFunction("createProvider");
export const declarationsEqual = hostOnlyAiFunction("declarationsEqual");
export const defaultProviderAuthContext = hostOnlyAiFunction("defaultProviderAuthContext");
export const envApiKeyAuth = hostOnlyAiFunction("envApiKeyAuth");
export const extractDiagnosticError = hostOnlyAiFunction("extractDiagnosticError");
export const fauxAssistantMessage = hostOnlyAiFunction("fauxAssistantMessage");
export const fauxProvider = hostOnlyAiFunction("fauxProvider");
export const fauxText = hostOnlyAiFunction("fauxText");
export const fauxThinking = hostOnlyAiFunction("fauxThinking");
export const fauxToolCall = hostOnlyAiFunction("fauxToolCall");
export const formatThrownValue = hostOnlyAiFunction("formatThrownValue");
export const getCurrentSystemMessage = hostOnlyAiFunction("getCurrentSystemMessage");
export const getCurrentSystemPrompt = hostOnlyAiFunction("getCurrentSystemPrompt");
export const getCurrentTools = hostOnlyAiFunction("getCurrentTools");
export const getDeclaredTools = hostOnlyAiFunction("getDeclaredTools");
export const getInitialSystemMessage = hostOnlyAiFunction("getInitialSystemMessage");
export const getOverflowPatterns = hostOnlyAiFunction("getOverflowPatterns");
export const getSupportedThinkingLevels = hostOnlyAiFunction("getSupportedThinkingLevels");
export const getToolStateChanges = hostOnlyAiFunction("getToolStateChanges");
export const hasApi = hostOnlyAiFunction("hasApi");
export const hasNonAdditiveToolChanges = hostOnlyAiFunction("hasNonAdditiveToolChanges");
export const hasToolRedefinitions = hostOnlyAiFunction("hasToolRedefinitions");
export const isContextOverflow = hostOnlyAiFunction("isContextOverflow");
export const isRecoverableLength = hostOnlyAiFunction("isRecoverableLength");
export const isRetryableAssistantError = hostOnlyAiFunction("isRetryableAssistantError");
export const lazyApi = hostOnlyAiFunction("lazyApi");
export const lazyOAuth = hostOnlyAiFunction("lazyOAuth");
export const lazyStream = hostOnlyAiFunction("lazyStream");
export const modelsAreEqual = hostOnlyAiFunction("modelsAreEqual");
export const normalizeContext = hostOnlyAiFunction("normalizeContext");
export const parseJsonWithRepair = hostOnlyAiFunction("parseJsonWithRepair");
export const parseStreamingJson = hostOnlyAiFunction("parseStreamingJson");
export const reduceAssistantMessageFrames = hostOnlyAiFunction("reduceAssistantMessageFrames");
export const registerSessionResourceCleanup = hostOnlyAiFunction("registerSessionResourceCleanup");
export const repairJson = hostOnlyAiFunction("repairJson");
export const resolveTranscript = hostOnlyAiFunction("resolveTranscript");
export const resolveTranscriptTools = hostOnlyAiFunction("resolveTranscriptTools");
export const retryAssistantCall = hostOnlyAiFunction("retryAssistantCall");
export const retryDelayMs = hostOnlyAiFunction("retryDelayMs");
export const toToolDeclaration = hostOnlyAiFunction("toToolDeclaration");
export const validateToolArguments = hostOnlyAiFunction("validateToolArguments");
export const validateToolCall = hostOnlyAiFunction("validateToolCall");
export const withoutInitialSystemMessage = hostOnlyAiFunction("withoutInitialSystemMessage");
