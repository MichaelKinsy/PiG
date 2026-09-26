import { getRuntime } from "../state.mjs";
import { Type } from "./typebox.mjs";

export { Type };

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

// Pi's own code for these, copied verbatim from the pinned release
// (automation/gen/vendor-pi-dist.sh).
export { lazyApi, lazyStream } from "./pi-dist/pi-ai/api/lazy.js";
export { defaultProviderAuthContext } from "./pi-dist/pi-ai/auth/context.js";
export { InMemoryCredentialStore } from "./pi-dist/pi-ai/auth/credential-store.js";
export { envApiKeyAuth, lazyOAuth } from "./pi-dist/pi-ai/auth/helpers.js";
export { createImagesModels, createImagesProvider } from "./pi-dist/pi-ai/images-models.js";
export {
  calculateCost,
  clampThinkingLevel,
  createModels,
  createProvider,
  getSupportedThinkingLevels,
  hasApi,
  ModelsError,
  modelsAreEqual,
} from "./pi-dist/pi-ai/models.js";
export { InMemoryModelsStore } from "./pi-dist/pi-ai/models-store.js";
export {
  createFauxCore,
  fauxAssistantMessage,
  fauxProvider,
  fauxText,
  fauxThinking,
  fauxToolCall,
} from "./pi-dist/pi-ai/providers/faux.js";
export { AssistantMessageFrameEncoder, reduceAssistantMessageFrames } from "./pi-dist/pi-ai/utils/assistant-message-frame.js";
export {
  appendAssistantMessageDiagnostic,
  createAssistantMessageDiagnostic,
  extractDiagnosticError,
  formatThrownValue,
} from "./pi-dist/pi-ai/utils/diagnostics.js";
export { AssistantMessageEventStream, createAssistantMessageEventStream, EventStream } from "./pi-dist/pi-ai/utils/event-stream.js";
export { parseJsonWithRepair, parseStreamingJson, repairJson } from "./pi-dist/pi-ai/utils/json-parse.js";
export { getOverflowPatterns, isContextOverflow, isRecoverableLength } from "./pi-dist/pi-ai/utils/overflow.js";
export {
  DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
  isRetryableAssistantError,
  retryAssistantCall,
  retryDelayMs,
} from "./pi-dist/pi-ai/utils/retry.js";
export { contentText, getSystemMessageText, renderSystemMessageUpdate } from "./pi-dist/pi-ai/utils/text.js";
export * from "./pi-dist/pi-ai/utils/transcript.js";
export { StringEnum } from "./pi-dist/pi-ai/utils/typebox-helpers.js";
export { uuidv7 } from "./pi-dist/pi-ai/utils/uuid.js";
export { validateToolArguments, validateToolCall } from "./pi-dist/pi-ai/utils/validation.js";

// pig divergence (D73): Pi runs these session-resource cleanups when its
// agent session ends. Sessions live in PiG's Go host, so a cleanup registered
// in the extension process would never run. The values are importable so no
// extension fails at link time, and throw a descriptive error only when
// called.
function hostOnlyAiFunction(name) {
  const fn = function () {
    throw new Error(`${name} is not available to extensions running in PiG: agent sessions run in PiG's host (see DIVERGENCES.md D73)`);
  };
  Object.defineProperty(fn, "name", { value: name });
  return fn;
}
export const cleanupSessionResources = hostOnlyAiFunction("cleanupSessionResources");
export const registerSessionResourceCleanup = hostOnlyAiFunction("registerSessionResourceCleanup");
