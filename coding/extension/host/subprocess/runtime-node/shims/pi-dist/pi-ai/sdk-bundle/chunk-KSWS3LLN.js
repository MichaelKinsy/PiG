import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/models.js
import { lazyStream } from "../api/lazy.js";

// pi-dist/pi-ai/auth/context.js
var __rewriteRelativeImportExtension = function(path, preserveJsx) {
  if (typeof path === "string" && /^\.\.?\//.test(path)) {
    return path.replace(/\.(tsx)$|((?:\.d)?)((?:\.[^./]+?)?)\.([cm]?)ts$/i, function(m, tsx, d, ext, cm) {
      return tsx ? preserveJsx ? ".jsx" : ".js" : d && (!ext || !cm) ? m : d + ext + "." + cm.toLowerCase() + "js";
    });
  }
  return path;
};
var importNodeModule = /* @__PURE__ */ __name((specifier) => import(__rewriteRelativeImportExtension(specifier)), "importNodeModule");
function getProcessEnv() {
  const proc = globalThis.process;
  return proc?.env;
}
__name(getProcessEnv, "getProcessEnv");
function defaultProviderAuthContext() {
  return {
    async env(name) {
      const value = getProcessEnv()?.[name];
      return typeof value === "string" && value.trim().length > 0 ? value : void 0;
    },
    async fileExists(path) {
      try {
        const fs = await importNodeModule("node:fs/promises");
        let resolved = path;
        if (resolved.startsWith("~")) {
          const os = await importNodeModule("node:os");
          resolved = os.homedir() + resolved.slice(1);
        }
        await fs.access(resolved);
        return true;
      } catch {
        return false;
      }
    }
  };
}
__name(defaultProviderAuthContext, "defaultProviderAuthContext");

// pi-dist/pi-ai/utils/abort.js
function abortReason(signal) {
  if (signal.reason !== void 0)
    return signal.reason;
  const error = new Error("The operation was aborted");
  error.name = "AbortError";
  return error;
}
__name(abortReason, "abortReason");
function operationSignal(signal) {
  return signal ?? new AbortController().signal;
}
__name(operationSignal, "operationSignal");
function raceWithAbortSignal(operation, signal) {
  if (signal.aborted) {
    void operation.catch(() => {
    });
    return Promise.reject(abortReason(signal));
  }
  return new Promise((resolve, reject) => {
    let settled = false;
    const cleanup = /* @__PURE__ */ __name(() => signal.removeEventListener("abort", onAbort), "cleanup");
    const onAbort = /* @__PURE__ */ __name(() => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(abortReason(signal));
    }, "onAbort");
    signal.addEventListener("abort", onAbort, { once: true });
    void operation.then((value) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      resolve(value);
    }, (error) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(error);
    });
    if (signal.aborted)
      onAbort();
  });
}
__name(raceWithAbortSignal, "raceWithAbortSignal");

// pi-dist/pi-ai/auth/credential-store.js
var InMemoryCredentialStore = class {
  static {
    __name(this, "InMemoryCredentialStore");
  }
  credentials = /* @__PURE__ */ new Map();
  chains = /* @__PURE__ */ new Map();
  /** Serialize tasks per provider id without releasing the chain before active work settles. */
  enqueue(providerId, task, options) {
    const signal = operationSignal(options?.signal);
    const previous = this.chains.get(providerId) ?? Promise.resolve();
    const queued = (async () => {
      await previous.catch(() => {
      });
      signal.throwIfAborted();
      return task();
    })();
    const tail = queued.catch(() => {
    });
    this.chains.set(providerId, tail);
    void tail.then(() => {
      if (this.chains.get(providerId) === tail)
        this.chains.delete(providerId);
    });
    return raceWithAbortSignal(queued, signal);
  }
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    return this.credentials.get(providerId);
  }
  async list(options) {
    options?.signal?.throwIfAborted();
    return [...this.credentials].map(([providerId, credential]) => ({ providerId, type: credential.type }));
  }
  modify(providerId, fn, options) {
    return this.enqueue(providerId, async () => {
      const current = this.credentials.get(providerId);
      const next = await fn(current);
      options?.signal?.throwIfAborted();
      if (next !== void 0)
        this.credentials.set(providerId, next);
      return next ?? current;
    }, options);
  }
  delete(providerId, options) {
    return this.enqueue(providerId, async () => {
      this.credentials.delete(providerId);
    }, options);
  }
};

// pi-dist/pi-ai/utils/diagnostics.js
function formatThrownValue(value) {
  if (value instanceof Error)
    return value.message || value.name;
  if (typeof value === "string")
    return value;
  return String(value);
}
__name(formatThrownValue, "formatThrownValue");
function extractDiagnosticError(error) {
  if (!(error instanceof Error))
    return { name: "ThrownValue", message: formatThrownValue(error) };
  const code = error.code;
  return {
    name: error.name || void 0,
    message: error.message || error.name,
    stack: error.stack,
    code: typeof code === "string" || typeof code === "number" ? code : void 0
  };
}
__name(extractDiagnosticError, "extractDiagnosticError");
function createAssistantMessageDiagnostic(type, error, details) {
  return { type, timestamp: Date.now(), error: extractDiagnosticError(error), details };
}
__name(createAssistantMessageDiagnostic, "createAssistantMessageDiagnostic");
function appendAssistantMessageDiagnostic(message, diagnostic) {
  message.diagnostics = [...message.diagnostics ?? [], diagnostic];
}
__name(appendAssistantMessageDiagnostic, "appendAssistantMessageDiagnostic");

// pi-dist/pi-ai/utils/models-error.js
var ModelsError = class extends Error {
  static {
    __name(this, "ModelsError");
  }
  code;
  constructor(code, message, options) {
    super(withCauseDetail(message, options?.cause), options);
    this.name = "ModelsError";
    this.code = code;
  }
};
function withCauseDetail(message, cause) {
  if (cause === void 0 || cause === null)
    return message;
  const detail = formatThrownValue(cause).trim();
  if (!detail || message.includes(detail))
    return message;
  return `${message}: ${detail}`;
}
__name(withCauseDetail, "withCauseDetail");

// pi-dist/pi-ai/auth/resolve.js
function resolveProviderAuth(provider2, credentials, authContext, overrides) {
  const signal = operationSignal(overrides?.signal);
  return raceWithAbortSignal(resolveProviderAuthWithSignal(provider2, credentials, authContext, overrides, signal), signal);
}
__name(resolveProviderAuth, "resolveProviderAuth");
async function resolveProviderAuthWithSignal(provider2, credentials, authContext, overrides, signal) {
  signal.throwIfAborted();
  const requestAuthContext = overrides?.env ? overlayEnvAuthContext(authContext, overrides.env) : authContext;
  if (overrides?.apiKey !== void 0 && provider2.auth.apiKey) {
    return resolveApiKey(requestAuthContext, provider2.auth.apiKey, provider2.id, {
      type: "api_key",
      key: overrides.apiKey,
      env: overrides.env
    }, signal);
  }
  const stored = await readCredential(credentials, provider2.id, signal);
  if (stored) {
    if (stored.type === "oauth" && provider2.auth.oauth) {
      return resolveStoredOAuth(credentials, provider2.id, provider2.auth.oauth, stored, signal, overrides?.minOAuthValidityMs);
    }
    if (stored.type === "api_key" && provider2.auth.apiKey) {
      const credential = overrides?.env ? { ...stored, env: { ...stored.env, ...overrides.env } } : stored;
      return resolveApiKey(requestAuthContext, provider2.auth.apiKey, provider2.id, credential, signal);
    }
    return void 0;
  }
  return provider2.auth.apiKey ? resolveApiKey(requestAuthContext, provider2.auth.apiKey, provider2.id, void 0, signal) : void 0;
}
__name(resolveProviderAuthWithSignal, "resolveProviderAuthWithSignal");
function overlayEnvAuthContext(base, env) {
  return {
    env: /* @__PURE__ */ __name(async (name) => env[name] || await base.env(name), "env"),
    fileExists: /* @__PURE__ */ __name((path) => base.fileExists(path), "fileExists")
  };
}
__name(overlayEnvAuthContext, "overlayEnvAuthContext");
var DEFAULT_OAUTH_MINIMUM_VALIDITY_MS = 5 * 60 * 1e3;
var DEFAULT_OAUTH_REFRESH_TIMEOUT_MS = 15e3;
async function refreshStoredOAuthCredential(credentials, providerId, oauth, needsRefresh, signal) {
  let post;
  const lockWait = new AbortController();
  const cancelLockWait = /* @__PURE__ */ __name(() => lockWait.abort(signal.reason), "cancelLockWait");
  signal.addEventListener("abort", cancelLockWait, { once: true });
  if (signal.aborted)
    cancelLockWait();
  try {
    post = await credentials.modify(providerId, async (current) => {
      signal.removeEventListener("abort", cancelLockWait);
      signal.throwIfAborted();
      if (current?.type !== "oauth")
        return void 0;
      if (!needsRefresh(current))
        return void 0;
      try {
        return await oauth.refresh(current, AbortSignal.timeout(DEFAULT_OAUTH_REFRESH_TIMEOUT_MS));
      } catch (error) {
        throw new ModelsError("oauth", `OAuth refresh failed for ${providerId}`, { cause: error });
      }
    }, { signal: lockWait.signal });
  } catch (error) {
    if (error instanceof ModelsError)
      throw error;
    signal.throwIfAborted();
    throw new ModelsError("auth", `Credential store modify failed for ${providerId}`, { cause: error });
  } finally {
    signal.removeEventListener("abort", cancelLockWait);
  }
  return post?.type === "oauth" ? post : void 0;
}
__name(refreshStoredOAuthCredential, "refreshStoredOAuthCredential");
async function resolveStoredOAuth(credentials, providerId, oauth, stored, signal, minOAuthValidityMs) {
  const minimumValidityMs = Math.max(DEFAULT_OAUTH_MINIMUM_VALIDITY_MS, minOAuthValidityMs ?? 0);
  const expiresSoon = /* @__PURE__ */ __name((credential2) => Date.now() + minimumValidityMs >= credential2.expires, "expiresSoon");
  let credential = stored;
  if (expiresSoon(credential)) {
    const post = await refreshStoredOAuthCredential(credentials, providerId, oauth, expiresSoon, signal);
    if (!post)
      return void 0;
    credential = post;
    if (minOAuthValidityMs !== void 0 && expiresSoon(credential)) {
      throw new ModelsError("oauth", `OAuth refresh returned a token that expires too soon for ${providerId}`);
    }
  }
  try {
    return { auth: await oauth.toAuth(credential), source: "OAuth" };
  } catch (error) {
    throw new ModelsError("oauth", `OAuth auth derivation failed for ${providerId}`, { cause: error });
  }
}
__name(resolveStoredOAuth, "resolveStoredOAuth");
async function resolveApiKey(authContext, apiKey, providerId, credential, signal) {
  try {
    return await apiKey.resolve({ ctx: authContext, credential, signal });
  } catch (error) {
    throw new ModelsError("auth", `API key auth failed for provider ${providerId}`, { cause: error });
  }
}
__name(resolveApiKey, "resolveApiKey");
async function readCredential(credentials, providerId, signal) {
  try {
    return await credentials.read(providerId, { signal });
  } catch (error) {
    throw new ModelsError("auth", `Credential store read failed for ${providerId}`, { cause: error });
  }
}
__name(readCredential, "readCredential");

// pi-dist/pi-ai/models-store.js
var InMemoryModelsStore = class {
  static {
    __name(this, "InMemoryModelsStore");
  }
  entries = /* @__PURE__ */ new Map();
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    const entry = this.entries.get(providerId);
    return entry ? structuredClone(entry) : void 0;
  }
  async write(providerId, entry, options) {
    options?.signal?.throwIfAborted();
    this.entries.set(providerId, structuredClone(entry));
  }
  async delete(providerId, options) {
    options?.signal?.throwIfAborted();
    this.entries.delete(providerId);
  }
};

// pi-dist/pi-ai/telemetry.js
import { createTypedMetrics, createTypedSpanStarter, defineTelemetrySchema, NOOP_TELEMETRY_CONTEXT, NOOP_TELEMETRY_METRICS } from "../../pi-telemetry/index.js";
var provider = { type: "string", description: "Provider identifier.", required: true };
var model = { type: "string", description: "Requested model identifier.", required: true };
var stop = {
  type: "string",
  description: "Normalized stop reason.",
  values: ["pending", "stop", "length", "toolUse", "error", "aborted", "deferred"]
};
var request = {
  description: "One logical model request, including authentication and provider setup.",
  parents: { kind: "any" },
  startAttributes: {
    provider,
    api: { type: "string", description: "API identifier.", required: true },
    model,
    reasoning: { type: "string", description: "Scalar requested reasoning level.", required: false }
  },
  endAttributes: {
    stop,
    "usage.input": { type: "number", description: "Uncached input tokens." },
    "usage.output": { type: "number", description: "Output tokens." },
    "usage.cache_read": { type: "number", description: "Cache-read tokens." },
    "usage.cache_write": { type: "number", description: "Cache-write tokens." },
    "cost.total": { type: "number", description: "Total request cost in USD." },
    "response.id": { type: "string", description: "Provider response identifier.", cardinality: "high" }
  },
  events: {
    "pi.ai.first_chunk": { description: "First normalized stream event delivered.", attributes: {} },
    "pi.ai.block": {
      description: "A completed content block; content is never recorded.",
      attributes: {
        type: {
          type: "string",
          description: "Block kind.",
          values: ["text", "thinking", "tool_call"],
          required: true
        },
        index: { type: "number", description: "Content block index.", required: true },
        chars: { type: "number", description: "UTF-16 character count.", required: true },
        tool: { type: "string", description: "Tool name, for tool-call blocks only.", required: false }
      }
    }
  },
  status: {
    default: "ok",
    errorWhen: "Stop reason is error: provider_error with at most 500 error-message characters. Aborted stays ok."
  }
};
var AI_TELEMETRY_SCHEMA = defineTelemetrySchema({
  version: 1,
  spans: {
    "pi.ai.request": request,
    "pi.ai.poll": { ...request, description: "One deferred-response poll, not the deferred request's lifetime." }
  },
  metrics: {
    "pi.ai.request.duration": {
      kind: "histogram",
      description: "Logical request or poll duration.",
      unit: "ms",
      attributes: { provider, model, stop: { ...stop, required: true } }
    },
    "pi.ai.first_chunk.duration": {
      kind: "histogram",
      description: "Time to the first normalized stream event.",
      unit: "ms",
      attributes: { provider, model }
    },
    "pi.ai.tokens": {
      kind: "counter",
      description: "Reported model token usage; pending and deferred polls are excluded.",
      unit: "token",
      attributes: {
        provider,
        model,
        kind: {
          type: "string",
          description: "Token usage category.",
          values: ["input", "output", "cache_read", "cache_write"],
          required: true
        }
      }
    },
    "pi.ai.cost": {
      kind: "counter",
      description: "Reported model cost; pending and deferred polls are excluded.",
      unit: "USD",
      attributes: { provider, model }
    }
  }
});
function createInstruments(adapter) {
  const metrics = createTypedMetrics(adapter, [AI_TELEMETRY_SCHEMA]);
  return {
    requestDuration: metrics.histogram("pi.ai.request.duration"),
    firstChunkDuration: metrics.histogram("pi.ai.first_chunk.duration"),
    tokens: metrics.counter("pi.ai.tokens"),
    cost: metrics.counter("pi.ai.cost")
  };
}
__name(createInstruments, "createInstruments");
var instrumentCache = /* @__PURE__ */ new WeakMap();
function observeModelRequest(requestedModel, options, poll) {
  const started = performance.now();
  const attributes = { provider: requestedModel.provider, model: requestedModel.id };
  const adapter = options?.telemetry?.metrics ?? NOOP_TELEMETRY_METRICS;
  let instruments = instrumentCache.get(adapter);
  if (!instruments) {
    instruments = createInstruments(adapter);
    instrumentCache.set(adapter, instruments);
  }
  const { requestDuration, firstChunkDuration, tokens, cost } = instruments;
  let span;
  let finish;
  let first = true;
  const toolChars = /* @__PURE__ */ new Map();
  const finished = new Promise((resolve) => {
    finish = resolve;
  });
  const run = /* @__PURE__ */ __name((activeSpan) => {
    span = activeSpan;
    return finished;
  }, "run");
  const startAttributes = {
    ...attributes,
    api: requestedModel.api,
    ...typeof options?.reasoning === "string" ? { reasoning: options.reasoning } : {}
  };
  const start = createTypedSpanStarter(options?.telemetry ?? NOOP_TELEMETRY_CONTEXT, [AI_TELEMETRY_SCHEMA]);
  if (poll)
    void start("pi.ai.poll", startAttributes, run);
  else
    void start("pi.ai.request", startAttributes, run);
  return {
    onEvent(event) {
      if (first) {
        first = false;
        span.addEvent("pi.ai.first_chunk");
        firstChunkDuration.record(performance.now() - started, attributes, span);
      }
      if (event.type === "toolcall_delta") {
        toolChars.set(event.contentIndex, (toolChars.get(event.contentIndex) ?? 0) + event.delta.length);
      } else if (event.type === "text_end" || event.type === "thinking_end") {
        span.addEvent("pi.ai.block", {
          type: event.type === "text_end" ? "text" : "thinking",
          index: event.contentIndex,
          chars: event.content.length
        });
      } else if (event.type === "toolcall_end") {
        const chars = toolChars.get(event.contentIndex) ?? 0;
        toolChars.delete(event.contentIndex);
        span.addEvent("pi.ai.block", {
          type: "tool_call",
          index: event.contentIndex,
          chars,
          tool: event.toolCall.name
        });
      }
    },
    onResult(message) {
      if (message) {
        span.setAttributes({
          stop: message.stopReason,
          "usage.input": message.usage.input,
          "usage.output": message.usage.output,
          "usage.cache_read": message.usage.cacheRead,
          "usage.cache_write": message.usage.cacheWrite,
          "cost.total": message.usage.cost.total,
          "response.id": message.responseId
        });
        if (message.stopReason === "error") {
          span.setStatus({
            status: "error",
            error: { name: "provider_error", message: message.errorMessage?.slice(0, 500) ?? "" }
          });
        }
        requestDuration.record(performance.now() - started, { ...attributes, stop: message.stopReason }, span);
        if (!poll || message.stopReason !== "pending" && message.stopReason !== "deferred") {
          tokens.add(message.usage.input, { ...attributes, kind: "input" });
          tokens.add(message.usage.output, { ...attributes, kind: "output" });
          tokens.add(message.usage.cacheRead, { ...attributes, kind: "cache_read" });
          tokens.add(message.usage.cacheWrite, { ...attributes, kind: "cache_write" });
          cost.add(message.usage.cost.total, attributes);
        }
      }
      finish();
    }
  };
}
__name(observeModelRequest, "observeModelRequest");

// pi-dist/pi-ai/utils/version.js
var PI_AI_VERSION = "1.4.0";

// pi-dist/pi-ai/utils/identity.js
import { pigUserAgent } from "../../../pig-identity.mjs";
var PI_ATTRIBUTION = { name: "PiG", url: "https://github.com/MichaelKinsy/PiG", categories: ["cli-agent"] };
var NVIDIA_NIM_HOST = "integrate.api.nvidia.com";
var OPENCODE_HOST = "opencode.ai";
function mergeClientIdentity(...sources) {
  let merged;
  for (const source of sources) {
    if (!source)
      continue;
    merged = { ...merged };
    if (source.userAgent !== void 0)
      merged.userAgent = source.userAgent;
    if (source.attribution !== void 0)
      merged.attribution = source.attribution;
    if (source.version !== void 0)
      merged.version = source.version;
  }
  return merged;
}
__name(mergeClientIdentity, "mergeClientIdentity");
function getClientVersion(identity) {
  return identity?.version ?? PI_AI_VERSION;
}
__name(getClientVersion, "getClientVersion");
function formatUserAgent(identity) {
  const mode = identity?.userAgent ?? "detailed";
  if (typeof mode === "object")
    return mode.custom;
  if (mode === "short")
    return "pig";
  return pigUserAgent("pig", identity?.version);
}
__name(formatUserAgent, "formatUserAgent");
function matchesHost(baseUrl, expectedHost) {
  try {
    return new URL(baseUrl).hostname === expectedHost;
  } catch {
    return false;
  }
}
__name(matchesHost, "matchesHost");
function isOpenRouter(target) {
  return target.provider === "openrouter" || target.baseUrl.includes("openrouter.ai");
}
__name(isOpenRouter, "isOpenRouter");
function isNvidiaNim(target) {
  return target.provider === "nvidia" || matchesHost(target.baseUrl, NVIDIA_NIM_HOST);
}
__name(isNvidiaNim, "isNvidiaNim");
function isOpenCode(target) {
  return target.provider === "opencode" || target.provider === "opencode-go" || matchesHost(target.baseUrl, OPENCODE_HOST);
}
__name(isOpenCode, "isOpenCode");
function getAttributionHeaders(target, identity) {
  const attribution = identity?.attribution;
  if (!attribution)
    return {};
  const { name, url, categories } = attribution;
  if (isOpenRouter(target)) {
    return {
      "HTTP-Referer": url,
      "X-OpenRouter-Title": name,
      ...categories === void 0 || categories.length === 0 ? {} : { "X-OpenRouter-Categories": categories.join(",") }
    };
  }
  if (isNvidiaNim(target))
    return { "X-BILLING-INVOKE-ORIGIN": name.charAt(0).toUpperCase() + name.slice(1) };
  if (isOpenCode(target))
    return { "x-opencode-client": name };
  return {};
}
__name(getAttributionHeaders, "getAttributionHeaders");
function getOpenCodeSessionHeaders(target, sessionId) {
  return sessionId && isOpenCode(target) ? { "x-opencode-session": sessionId } : void 0;
}
__name(getOpenCodeSessionHeaders, "getOpenCodeSessionHeaders");
function getIdentityHeaders(target, identity) {
  return { "User-Agent": formatUserAgent(identity), ...getAttributionHeaders(target, identity) };
}
__name(getIdentityHeaders, "getIdentityHeaders");
function isMutableModels(models) {
  return "setProvider" in models && "deleteProvider" in models && "clearProviders" in models;
}
__name(isMutableModels, "isMutableModels");
function withClientIdentity(models, identity) {
  const withIdentity = /* @__PURE__ */ __name((options) => ({ ...options, identity: mergeClientIdentity(identity, options?.identity) }), "withIdentity");
  const wrapped = {
    getProviders: /* @__PURE__ */ __name(() => models.getProviders(), "getProviders"),
    getProvider: /* @__PURE__ */ __name((id) => models.getProvider(id), "getProvider"),
    getModels: /* @__PURE__ */ __name((provider2) => models.getModels(provider2), "getModels"),
    getModel: /* @__PURE__ */ __name((provider2, id) => models.getModel(provider2, id), "getModel"),
    getModelsOfType: /* @__PURE__ */ __name((type, provider2) => models.getModelsOfType(type, provider2), "getModelsOfType"),
    getModelOfType: /* @__PURE__ */ __name((type, provider2, id) => models.getModelOfType(type, provider2, id), "getModelOfType"),
    getAllModels: /* @__PURE__ */ __name((provider2) => models.getAllModels(provider2), "getAllModels"),
    refresh: /* @__PURE__ */ __name((options) => models.refresh(options), "refresh"),
    checkAuth: /* @__PURE__ */ __name((providerId, options) => models.checkAuth(providerId, options), "checkAuth"),
    getAvailable: /* @__PURE__ */ __name((providerId, options) => models.getAvailable(providerId, options), "getAvailable"),
    getAvailableOfType: /* @__PURE__ */ __name((type, providerId, options) => models.getAvailableOfType(type, providerId, options), "getAvailableOfType"),
    getAllAvailable: /* @__PURE__ */ __name((providerId, options) => models.getAllAvailable(providerId, options), "getAllAvailable"),
    getAuth: models.getAuth.bind(models),
    login: /* @__PURE__ */ __name((providerId, type, interaction, options) => models.login(providerId, type, interaction, options), "login"),
    logout: /* @__PURE__ */ __name((providerId, options) => models.logout(providerId, options), "logout"),
    stream: /* @__PURE__ */ __name((model2, context, options) => models.stream(model2, context, withIdentity(options)), "stream"),
    complete: /* @__PURE__ */ __name((model2, context, options) => models.complete(model2, context, withIdentity(options)), "complete"),
    streamSimple: /* @__PURE__ */ __name((model2, context, options) => models.streamSimple(model2, context, withIdentity(options)), "streamSimple"),
    completeSimple: /* @__PURE__ */ __name((model2, context, options) => models.completeSimple(model2, context, withIdentity(options)), "completeSimple"),
    streamDeferred: /* @__PURE__ */ __name((model2, handle, options) => models.streamDeferred(model2, handle, withIdentity(options)), "streamDeferred"),
    fetchDeferred: /* @__PURE__ */ __name((model2, handle, options) => models.fetchDeferred(model2, handle, withIdentity(options)), "fetchDeferred"),
    cancelDeferred: /* @__PURE__ */ __name((model2, handle, options) => models.cancelDeferred(model2, handle, withIdentity(options)), "cancelDeferred"),
    generateImages: /* @__PURE__ */ __name((model2, context, options) => models.generateImages(model2, context, withIdentity(options)), "generateImages"),
    classify: /* @__PURE__ */ __name((model2, context, options) => models.classify(model2, context, withIdentity(options)), "classify")
  };
  if (!isMutableModels(models))
    return wrapped;
  const mutable = {
    ...wrapped,
    setProvider: /* @__PURE__ */ __name((provider2) => models.setProvider(provider2), "setProvider"),
    deleteProvider: /* @__PURE__ */ __name((id) => models.deleteProvider(id), "deleteProvider"),
    clearProviders: /* @__PURE__ */ __name(() => models.clearProviders(), "clearProviders")
  };
  return mutable;
}
__name(withClientIdentity, "withClientIdentity");

// pi-dist/pi-ai/utils/model-operations.js
function getModelType(model2) {
  return model2.type ?? "chat";
}
__name(getModelType, "getModelType");
function isModelType(model2, type) {
  return getModelType(model2) === type;
}
__name(isModelType, "isModelType");
function assertChatModel(model2) {
  if (!isModelType(model2, "chat")) {
    throw new ModelsError("provider", `Model ${model2.provider}/${model2.id} is not a chat model`);
  }
}
__name(assertChatModel, "assertChatModel");
function assertImageModel(model2) {
  if (!isModelType(model2, "image")) {
    throw new ModelsError("provider", `Model ${model2.provider}/${model2.id} is not an image model`);
  }
}
__name(assertImageModel, "assertImageModel");
function assertClassifierModel(model2) {
  if (!isModelType(model2, "classifier")) {
    throw new ModelsError("provider", `Model ${model2.provider}/${model2.id} is not a classifier model`);
  }
}
__name(assertClassifierModel, "assertClassifierModel");
function assertClassifierInputSupported(model2, context) {
  if (context.images?.length && !model2.input.includes("image")) {
    throw new ModelsError("provider", `Model ${model2.provider}/${model2.id} does not accept image input`);
  }
}
__name(assertClassifierInputSupported, "assertClassifierInputSupported");
function imageErrorResult(model2, error, aborted = false) {
  return {
    api: model2.api,
    provider: model2.provider,
    model: model2.id,
    output: [],
    stopReason: aborted ? "aborted" : "error",
    errorMessage: error instanceof Error ? error.message : String(error),
    timestamp: Date.now()
  };
}
__name(imageErrorResult, "imageErrorResult");
function classifierErrorResult(model2, error, aborted = false) {
  return {
    api: model2.api,
    provider: model2.provider,
    model: model2.id,
    answers: {},
    stopReason: aborted ? "aborted" : "error",
    errorMessage: error instanceof Error ? error.message : String(error),
    timestamp: Date.now()
  };
}
__name(classifierErrorResult, "classifierErrorResult");

// pi-dist/pi-ai/utils/text.js
function contentText(content, separator = "\n") {
  if (typeof content === "string")
    return content;
  return content.filter((block) => block.type === "text").map((block) => block.text).join(separator);
}
__name(contentText, "contentText");
function getSystemMessageText(message) {
  const parts = [contentText(message.content)];
  for (const text of Object.values(message.sections ?? {})) {
    if (text !== null)
      parts.push(text);
  }
  return parts.filter((part) => part.length > 0).join("\n\n");
}
__name(getSystemMessageText, "getSystemMessageText");
function renderSystemMessageUpdate(message) {
  const parts = [];
  const text = contentText(message.content);
  if (text.length > 0)
    parts.push(text);
  for (const [name, value] of Object.entries(message.sections ?? {})) {
    parts.push(value === null ? `Removed system prompt section "${name}".` : `Updated system prompt section "${name}":

${value}`);
  }
  return parts.join("\n\n");
}
__name(renderSystemMessageUpdate, "renderSystemMessageUpdate");

// pi-dist/pi-ai/utils/transcript.js
function createInitialSystemMessage(systemPrompt, tools) {
  const hasSystemPrompt = systemPrompt !== void 0 && systemPrompt.length > 0;
  const hasTools = tools !== void 0 && tools.length > 0;
  if (!hasSystemPrompt && !hasTools)
    return void 0;
  return {
    role: "system",
    content: systemPrompt ?? "",
    ...hasTools ? { toolsAdded: tools } : {},
    timestamp: 0
  };
}
__name(createInitialSystemMessage, "createInitialSystemMessage");
function normalizeContext(context) {
  const initialMessage = createInitialSystemMessage(context.systemPrompt, context.tools);
  const messages = initialMessage ? [initialMessage, ...context.messages] : context.messages;
  return { messages };
}
__name(normalizeContext, "normalizeContext");
function isSystemMessage(message) {
  return message.role === "system";
}
__name(isSystemMessage, "isSystemMessage");
function getInitialSystemMessage(messages) {
  const first = messages[0];
  return first && isSystemMessage(first) ? first : void 0;
}
__name(getInitialSystemMessage, "getInitialSystemMessage");
function withoutInitialSystemMessage(messages) {
  return getInitialSystemMessage(messages) ? messages.slice(1) : messages;
}
__name(withoutInitialSystemMessage, "withoutInitialSystemMessage");
function getCurrentTools(messages) {
  const tools = /* @__PURE__ */ new Map();
  for (const message of messages) {
    if (!isSystemMessage(message))
      continue;
    for (const tool of message.toolsRemoved ?? [])
      tools.delete(tool.name);
    for (const tool of message.toolsAdded ?? [])
      tools.set(tool.name, tool);
  }
  return [...tools.values()];
}
__name(getCurrentTools, "getCurrentTools");
function getCurrentSystemMessage(messages) {
  const content = [];
  const sections = /* @__PURE__ */ new Map();
  let timestamp;
  for (const message of messages) {
    if (!isSystemMessage(message))
      continue;
    timestamp ??= message.timestamp;
    const text = contentText(message.content);
    if (text.length > 0)
      content.push(text);
    for (const [name, value] of Object.entries(message.sections ?? {})) {
      if (value === null)
        sections.delete(name);
      else
        sections.set(name, value);
    }
  }
  const tools = getCurrentTools(messages);
  if (timestamp === void 0 && tools.length === 0)
    return void 0;
  return {
    role: "system",
    content: content.join("\n\n"),
    ...sections.size > 0 ? { sections: Object.fromEntries(sections) } : {},
    ...tools.length > 0 ? { toolsAdded: tools } : {},
    timestamp: timestamp ?? 0
  };
}
__name(getCurrentSystemMessage, "getCurrentSystemMessage");
function getCurrentSystemPrompt(messages) {
  const message = getCurrentSystemMessage(messages);
  return message ? getSystemMessageText(message) : "";
}
__name(getCurrentSystemPrompt, "getCurrentSystemPrompt");
function collapseSystemMessages(context) {
  const head = getCurrentSystemMessage(context.messages);
  const messages = context.messages.filter((message) => message.role !== "system");
  return { messages: head ? [head, ...messages] : messages };
}
__name(collapseSystemMessages, "collapseSystemMessages");
function resolveTranscript(context, supportsMidConvoSystemMessages) {
  return supportsMidConvoSystemMessages ? context : collapseSystemMessages(context);
}
__name(resolveTranscript, "resolveTranscript");
function toToolDeclaration(tool) {
  return {
    name: tool.name,
    description: tool.description,
    parameters: JSON.parse(JSON.stringify(tool.parameters)),
    ...tool.constrainedSampling === void 0 ? {} : { constrainedSampling: tool.constrainedSampling }
  };
}
__name(toToolDeclaration, "toToolDeclaration");
function declarationsEqual(left, right) {
  return JSON.stringify(toToolDeclaration(left)) === JSON.stringify(toToolDeclaration(right));
}
__name(declarationsEqual, "declarationsEqual");
function getToolStateChanges(previous, current) {
  const previousTools = new Map(previous.map((tool) => [tool.name, tool]));
  const currentTools = new Map(current.map((tool) => [tool.name, tool]));
  return {
    toolsAdded: current.filter((tool) => {
      const previousTool = previousTools.get(tool.name);
      return previousTool === void 0 || !declarationsEqual(previousTool, tool);
    }).map(toToolDeclaration),
    toolsRemoved: previous.filter((tool) => {
      const currentTool = currentTools.get(tool.name);
      return currentTool === void 0 || !declarationsEqual(tool, currentTool);
    }).map((tool) => ({ name: tool.name }))
  };
}
__name(getToolStateChanges, "getToolStateChanges");
function getDeclaredTools(messages) {
  const definitions = /* @__PURE__ */ new Map();
  for (const message of messages) {
    if (!isSystemMessage(message))
      continue;
    for (const tool of message.toolsAdded ?? [])
      definitions.set(tool.name, tool);
  }
  return [...definitions.values()];
}
__name(getDeclaredTools, "getDeclaredTools");
function hasToolRedefinitions(messages) {
  const declared = /* @__PURE__ */ new Map();
  for (const message of messages) {
    if (!isSystemMessage(message))
      continue;
    for (const tool of message.toolsAdded ?? []) {
      const previous = declared.get(tool.name);
      if (previous !== void 0 && !declarationsEqual(previous, tool))
        return true;
      declared.set(tool.name, tool);
    }
  }
  return false;
}
__name(hasToolRedefinitions, "hasToolRedefinitions");
function hasNonAdditiveToolChanges(messages) {
  return findLastNonAdditiveToolChange(messages.filter(isSystemMessage)) >= 0;
}
__name(hasNonAdditiveToolChanges, "hasNonAdditiveToolChanges");
function findLastNonAdditiveToolChange(systemMessages) {
  const declared = /* @__PURE__ */ new Set();
  let last = -1;
  for (let index = 0; index < systemMessages.length; index++) {
    const message = systemMessages[index];
    if ((message.toolsRemoved?.length ?? 0) > 0)
      last = index;
    for (const tool of message.toolsAdded ?? []) {
      if (declared.has(tool.name))
        last = index;
      declared.add(tool.name);
    }
  }
  return last;
}
__name(findLastNonAdditiveToolChange, "findLastNonAdditiveToolChange");
function resolveTranscriptTools(messages, support) {
  const systemMessages = messages.filter(isSystemMessage);
  if (support === "none") {
    return { requestTools: getCurrentTools(systemMessages), foldedSystemMessages: systemMessages.length };
  }
  if (support === "changes")
    return { requestTools: [], foldedSystemMessages: 0 };
  const folded = Math.max(getInitialSystemMessage(messages) ? 1 : 0, findLastNonAdditiveToolChange(systemMessages) + 1);
  return { requestTools: getCurrentTools(systemMessages.slice(0, folded)), foldedSystemMessages: folded };
}
__name(resolveTranscriptTools, "resolveTranscriptTools");

// pi-dist/pi-ai/models.js
var KNOWN_MODEL_TYPES = { chat: true, image: true, classifier: true };
function hasKnownModelType(model2) {
  return Object.hasOwn(KNOWN_MODEL_TYPES, getModelType(model2));
}
__name(hasKnownModelType, "hasKnownModelType");
function withKnownModelTypes(entry) {
  return { ...entry, models: entry.models.filter(hasKnownModelType) };
}
__name(withKnownModelTypes, "withKnownModelTypes");
function mergeHeaders(base, override) {
  if (!base && !override)
    return void 0;
  const merged = { ...base };
  for (const [name, value] of Object.entries(override ?? {})) {
    const lowerName = name.toLowerCase();
    for (const existingName of Object.keys(merged)) {
      if (existingName.toLowerCase() === lowerName)
        delete merged[existingName];
    }
    merged[name] = value;
  }
  return merged;
}
__name(mergeHeaders, "mergeHeaders");
var ModelsImpl = class {
  static {
    __name(this, "ModelsImpl");
  }
  providers = /* @__PURE__ */ new Map();
  credentials;
  modelsStore;
  authContext;
  identity;
  refreshGenerations = /* @__PURE__ */ new Map();
  refreshControllers = /* @__PURE__ */ new Map();
  publicationChains = /* @__PURE__ */ new Map();
  constructor(options) {
    this.credentials = options?.credentials ?? new InMemoryCredentialStore();
    this.modelsStore = options?.modelsStore ?? new InMemoryModelsStore();
    this.authContext = options?.authContext ?? defaultProviderAuthContext();
    this.identity = options?.identity;
  }
  setProvider(provider2) {
    this.supersedeProviderRefresh(provider2.id);
    this.providers.set(provider2.id, provider2);
  }
  deleteProvider(id) {
    this.supersedeProviderRefresh(id);
    this.providers.delete(id);
  }
  clearProviders() {
    for (const id of /* @__PURE__ */ new Set([...this.providers.keys(), ...this.refreshControllers.keys()])) {
      this.supersedeProviderRefresh(id);
    }
    this.providers.clear();
  }
  getProviders() {
    return Array.from(this.providers.values());
  }
  getProvider(id) {
    return this.providers.get(id);
  }
  getModels(provider2) {
    if (provider2 !== void 0) {
      const entry = this.providers.get(provider2);
      if (!entry)
        return [];
      try {
        return entry.getModels();
      } catch {
        return [];
      }
    }
    const models = [];
    for (const entry of this.providers.values()) {
      try {
        models.push(...entry.getModels());
      } catch {
      }
    }
    return models;
  }
  getAllModels(provider2) {
    if (provider2 !== void 0) {
      const entry = this.providers.get(provider2);
      if (!entry)
        return [];
      try {
        return entry.getAllModels?.() ?? entry.getModels();
      } catch {
        return [];
      }
    }
    const models = [];
    for (const entry of this.providers.values()) {
      try {
        models.push(...entry.getAllModels?.() ?? entry.getModels());
      } catch {
      }
    }
    return models;
  }
  getModelsOfType(type, provider2) {
    return this.getAllModels(provider2).filter((model2) => isModelType(model2, type));
  }
  getModel(provider2, id) {
    return this.getModels(provider2).find((model2) => model2.id === id);
  }
  getModelOfType(type, provider2, id) {
    return this.getModelsOfType(type, provider2).find((model2) => model2.id === id);
  }
  supersedeProviderRefresh(providerId) {
    const generation = (this.refreshGenerations.get(providerId) ?? 0) + 1;
    this.refreshGenerations.set(providerId, generation);
    const previous = this.refreshControllers.get(providerId);
    if (previous) {
      this.refreshControllers.delete(providerId);
      previous.abort();
    }
    return generation;
  }
  beginProviderRefresh(providerId) {
    const generation = this.supersedeProviderRefresh(providerId);
    const controller = new AbortController();
    this.refreshControllers.set(providerId, controller);
    return { generation, controller };
  }
  publishProviderModels(providerId, generation, signal, publication) {
    const previous = this.publicationChains.get(providerId) ?? Promise.resolve();
    const queued = (async () => {
      await previous.catch(() => {
      });
      if (signal.aborted || this.refreshGenerations.get(providerId) !== generation)
        return false;
      if (publication.persist === null) {
        await this.modelsStore.delete(providerId, { signal });
      } else if (publication.persist !== void 0) {
        await this.modelsStore.write(providerId, structuredClone(publication.persist), { signal });
      }
      if (signal.aborted || this.refreshGenerations.get(providerId) !== generation)
        return false;
      publication.update?.();
      return true;
    })();
    const tail = queued.catch(() => {
    });
    this.publicationChains.set(providerId, tail);
    void tail.then(() => {
      if (this.publicationChains.get(providerId) === tail)
        this.publicationChains.delete(providerId);
    });
    return raceWithAbortSignal(queued, signal);
  }
  async runProviderRefreshPhase(provider2, credential, allowNetwork, force, generation, signal) {
    const stored = await this.modelsStore.read(provider2.id, { signal });
    await provider2.refreshModels({
      credential,
      stored: stored ? withKnownModelTypes(structuredClone(stored)) : void 0,
      publish: /* @__PURE__ */ __name((publication) => this.publishProviderModels(provider2.id, generation, signal, publication), "publish"),
      allowNetwork,
      force: allowNetwork ? force : void 0,
      signal,
      baseUrl: provider2.baseUrl
    });
  }
  async refresh(options = {}) {
    const allowNetwork = options.allowNetwork ?? true;
    const callerSignal = operationSignal(options.signal);
    const errors = /* @__PURE__ */ new Map();
    if (callerSignal.aborted)
      return { aborted: true, errors };
    const selected = options.providers ? new Set(options.providers) : void 0;
    const refreshable = Array.from(this.providers.values()).filter((provider2) => provider2.refreshModels !== void 0 && (!selected || selected.has(provider2.id)));
    const refresh = Promise.all(refreshable.map(async (provider2) => {
      const { generation, controller } = this.beginProviderRefresh(provider2.id);
      const signal = AbortSignal.any([callerSignal, controller.signal]);
      const operation = (async () => {
        let storedCredential;
        let credentialError;
        try {
          storedCredential = await this.readCredential(provider2.id, signal);
        } catch (error) {
          credentialError = error;
        }
        await this.runProviderRefreshPhase(provider2, storedCredential, false, void 0, generation, signal);
        if (credentialError !== void 0)
          throw credentialError;
        if (!allowNetwork || signal.aborted)
          return;
        const credential = await this.resolveRefreshCredential(provider2, storedCredential, signal);
        if (!credential)
          return;
        await this.runProviderRefreshPhase(provider2, credential, true, options.force, generation, signal);
      })();
      try {
        await raceWithAbortSignal(operation, signal);
      } catch (error) {
        if (!signal.aborted) {
          errors.set(provider2.id, error instanceof Error ? error : new ModelsError("model_source", `Model refresh failed for ${provider2.id}`, { cause: error }));
        }
      } finally {
        if (this.refreshControllers.get(provider2.id) === controller) {
          this.refreshControllers.delete(provider2.id);
        }
      }
    }));
    try {
      await raceWithAbortSignal(refresh, callerSignal);
    } catch (error) {
      if (!callerSignal.aborted)
        throw error;
    }
    return { aborted: callerSignal.aborted, errors: new Map(errors) };
  }
  async resolveRefreshCredential(provider2, stored, signal) {
    if (stored?.type === "oauth") {
      const oauth = provider2.auth.oauth;
      if (!oauth)
        return void 0;
      if (Date.now() < stored.expires)
        return stored;
      if (signal.aborted)
        return void 0;
      return refreshStoredOAuthCredential(this.credentials, provider2.id, oauth, (current) => Date.now() >= current.expires, signal);
    }
    const apiKey = provider2.auth.apiKey;
    if (!apiKey)
      return void 0;
    const credential = stored?.type === "api_key" ? stored : void 0;
    const result = await apiKey.resolve({ ctx: this.authContext, credential, signal });
    if (!result)
      return void 0;
    return { type: "api_key", key: result.auth.apiKey, env: result.env };
  }
  async readCredential(providerId, signal) {
    try {
      return await this.credentials.read(providerId, { signal });
    } catch (error) {
      throw new ModelsError("auth", `Credential store read failed for ${providerId}`, { cause: error });
    }
  }
  async checkProviderAuth(provider2, credential, signal) {
    if (credential?.type === "oauth") {
      return provider2.auth.oauth ? { source: "OAuth", type: "oauth" } : void 0;
    }
    const apiKey = provider2.auth.apiKey;
    if (!apiKey)
      return void 0;
    if (apiKey.check) {
      try {
        return await apiKey.check({
          ctx: this.authContext,
          credential: credential?.type === "api_key" ? credential : void 0,
          signal
        });
      } catch (error) {
        throw new ModelsError("auth", `API key auth check failed for provider ${provider2.id}`, { cause: error });
      }
    }
    const resolution = await resolveProviderAuth(provider2, this.credentials, this.authContext, { signal });
    return resolution ? { source: resolution.source, type: "api_key" } : void 0;
  }
  checkAuth(providerId, options) {
    const signal = operationSignal(options?.signal);
    const check = (async () => {
      signal.throwIfAborted();
      const provider2 = this.providers.get(providerId);
      if (!provider2)
        return void 0;
      return this.checkProviderAuth(provider2, await this.readCredential(providerId, signal), signal);
    })();
    return raceWithAbortSignal(check, signal);
  }
  async getAuthenticatedProviders(providerId, signal) {
    signal.throwIfAborted();
    const providers = providerId ? [this.providers.get(providerId)].filter((entry) => entry !== void 0) : this.getProviders();
    const checks = await Promise.all(providers.map(async (provider2) => {
      const credential = await this.readCredential(provider2.id, signal);
      return { provider: provider2, credential, auth: await this.checkProviderAuth(provider2, credential, signal) };
    }));
    return checks.filter((entry) => entry.auth !== void 0);
  }
  getAvailable(providerId, options) {
    const signal = operationSignal(options?.signal);
    const available = (async () => {
      const providers = await this.getAuthenticatedProviders(providerId, signal);
      return providers.flatMap(({ provider: provider2, credential }) => {
        const models = provider2.getModels();
        return provider2.filterModels?.(models, credential) ?? models;
      });
    })();
    return raceWithAbortSignal(available, signal);
  }
  async getAvailableOfType(type, providerId, options) {
    return (await this.getAllAvailable(providerId, options)).filter((model2) => isModelType(model2, type));
  }
  getAllAvailable(providerId, options) {
    const signal = operationSignal(options?.signal);
    const available = (async () => {
      const providers = await this.getAuthenticatedProviders(providerId, signal);
      return providers.flatMap(({ provider: provider2, credential }) => {
        const models = provider2.getAllModels?.() ?? provider2.getModels();
        if (provider2.filterAllModels)
          return provider2.filterAllModels(models, credential);
        if (!provider2.filterModels)
          return models;
        const availableChatIds = new Set(provider2.filterModels(provider2.getModels(), credential).map((model2) => model2.id));
        return models.filter((model2) => !isModelType(model2, "chat") || availableChatIds.has(model2.id));
      });
    })();
    return raceWithAbortSignal(available, signal);
  }
  async getAuth(providerOrModel, overrides) {
    const signal = operationSignal(overrides?.signal);
    const providerId = typeof providerOrModel === "string" ? providerOrModel : providerOrModel.provider;
    const provider2 = this.providers.get(providerId);
    if (!provider2)
      return void 0;
    const result = await resolveProviderAuth(provider2, this.credentials, this.authContext, { ...overrides, signal });
    if (!result || typeof providerOrModel === "string" || !providerOrModel.headers)
      return result;
    return {
      ...result,
      auth: {
        ...result.auth,
        headers: mergeHeaders(result.auth.headers, providerOrModel.headers)
      }
    };
  }
  async login(providerId, type, interaction, options) {
    const signal = operationSignal(interaction.signal);
    signal.throwIfAborted();
    const provider2 = this.providers.get(providerId);
    if (!provider2)
      throw new ModelsError("provider", `Unknown provider: ${providerId}`);
    const method = type === "oauth" ? provider2.auth.oauth : provider2.auth.apiKey;
    if (!method?.login) {
      throw new ModelsError("auth", `${provider2.name} does not support ${type} login`);
    }
    const loginOperation = method.login({ ...interaction, signal }, options);
    const credential = await raceWithAbortSignal(loginOperation, signal);
    let mutationStarted = false;
    let markMutationStarted;
    const started = new Promise((resolve) => {
      markMutationStarted = resolve;
    });
    const mutation = this.credentials.modify(providerId, async () => {
      mutationStarted = true;
      markMutationStarted?.();
      return credential;
    }, { signal });
    void mutation.catch(() => {
    });
    try {
      await new Promise((resolve, reject) => {
        const onAbort = /* @__PURE__ */ __name(() => {
          if (!mutationStarted)
            reject(signal.reason);
        }, "onAbort");
        signal.addEventListener("abort", onAbort, { once: true });
        void Promise.race([started, mutation]).then(() => {
          signal.removeEventListener("abort", onAbort);
          resolve();
        }, (error) => {
          signal.removeEventListener("abort", onAbort);
          reject(error);
        });
        if (signal.aborted)
          onAbort();
      });
      await mutation;
    } catch (error) {
      signal.throwIfAborted();
      throw new ModelsError("auth", `Credential store modify failed for ${providerId}`, { cause: error });
    }
    return credential;
  }
  async logout(providerId, options) {
    const signal = operationSignal(options?.signal);
    signal.throwIfAborted();
    try {
      await this.credentials.delete(providerId, { signal });
    } catch (error) {
      signal.throwIfAborted();
      throw new ModelsError("auth", `Credential store delete failed for ${providerId}`, { cause: error });
    }
  }
  requireProvider(model2) {
    const provider2 = this.providers.get(model2.provider);
    if (!provider2) {
      throw new ModelsError("provider", `Unknown provider: ${model2.provider}`);
    }
    return provider2;
  }
  requireChatProvider(model2) {
    assertChatModel(model2);
    return this.requireProvider(model2);
  }
  async applyAuth(model2, options) {
    this.requireProvider(model2);
    const resolution = await this.getAuth(model2, {
      apiKey: options?.apiKey,
      env: options?.env,
      signal: options?.signal
    });
    if (!resolution) {
      throw new ModelsError("auth", `Provider is not configured: ${model2.provider}`);
    }
    const auth = resolution.auth;
    const apiKey = options?.apiKey ?? auth.apiKey;
    const requestModel = auth.baseUrl ? { ...model2, baseUrl: auth.baseUrl } : model2;
    const sessionId = options && "sessionId" in options && typeof options.sessionId === "string" ? options.sessionId : void 0;
    let headers = mergeHeaders(mergeHeaders(getOpenCodeSessionHeaders(requestModel, sessionId), auth.headers), options?.headers);
    if (options?.transformHeaders)
      headers = await options.transformHeaders(headers ?? {});
    const env = resolution.env || options?.env ? { ...resolution.env ?? {}, ...options?.env ?? {} } : void 0;
    const identity = mergeClientIdentity(this.identity, options?.identity);
    const { transformHeaders: _transformHeaders, ...providerOptions } = options ?? {};
    const requestOptions = {
      ...providerOptions,
      apiKey,
      headers,
      env,
      ...identity === void 0 ? {} : { identity }
    };
    return { requestModel, requestOptions };
  }
  stream(model2, context, options) {
    const transcript = normalizeContext(context);
    return lazyStream(model2, async () => {
      const provider2 = this.requireChatProvider(model2);
      const { requestModel, requestOptions } = await this.applyAuth(model2, options);
      return provider2.stream(requestModel, transcript, requestOptions);
    }, observeModelRequest(model2, options, false));
  }
  async complete(model2, context, options) {
    return this.stream(model2, context, options).result();
  }
  streamSimple(model2, context, options) {
    const transcript = normalizeContext(context);
    return lazyStream(model2, async () => {
      const provider2 = this.requireChatProvider(model2);
      const { requestModel, requestOptions } = await this.applyAuth(model2, options);
      return provider2.streamSimple(requestModel, transcript, requestOptions);
    }, observeModelRequest(model2, options, false));
  }
  async completeSimple(model2, context, options) {
    return this.streamSimple(model2, context, options).result();
  }
  streamDeferred(model2, handle, options) {
    return lazyStream(model2, async () => {
      const provider2 = this.requireChatProvider(model2);
      if (!provider2.fetchDeferred) {
        throw new ModelsError("provider", `Provider ${model2.provider} does not support deferred responses`);
      }
      const { requestModel, requestOptions } = await this.applyAuth(model2, options);
      return provider2.fetchDeferred(requestModel, handle, requestOptions);
    }, observeModelRequest(model2, options, true));
  }
  async fetchDeferred(model2, handle, options) {
    return this.streamDeferred(model2, handle, options).result();
  }
  async cancelDeferred(model2, handle, options) {
    const provider2 = this.requireChatProvider(model2);
    if (!provider2.cancelDeferred) {
      throw new ModelsError("provider", `Provider ${model2.provider} does not support deferred responses`);
    }
    const { requestModel, requestOptions } = await this.applyAuth(model2, options);
    await provider2.cancelDeferred(requestModel, handle, requestOptions);
  }
  async generateImages(model2, context, options) {
    try {
      assertImageModel(model2);
      const provider2 = this.requireProvider(model2);
      if (!provider2.generateImages) {
        throw new ModelsError("provider", `Provider ${model2.provider} does not support image generation`);
      }
      const { requestModel, requestOptions } = await this.applyAuth(model2, options);
      return await provider2.generateImages(requestModel, context, requestOptions);
    } catch (error) {
      return imageErrorResult(model2, error, options?.signal?.aborted);
    }
  }
  async classify(model2, context, options) {
    try {
      assertClassifierModel(model2);
      assertClassifierInputSupported(model2, context);
      const provider2 = this.requireProvider(model2);
      if (!provider2.classify) {
        throw new ModelsError("provider", `Provider ${model2.provider} does not support classification`);
      }
      const { requestModel, requestOptions } = await this.applyAuth(model2, options);
      return await provider2.classify(requestModel, context, requestOptions);
    } catch (error) {
      return classifierErrorResult(model2, error, options?.signal?.aborted);
    }
  }
};
function createModels(options) {
  return new ModelsImpl(options);
}
__name(createModels, "createModels");
function createProvider(input) {
  const single = input.api && typeof input.api.stream === "function" ? input.api : void 0;
  const byApi = single || !input.api ? void 0 : input.api;
  const images = input.images;
  const classifiers = input.classifiers;
  const streams = single ? [single] : Object.values(byApi ?? {}).filter((entry) => entry !== void 0);
  const imageImplementations = Object.values(images ?? {}).filter((entry) => entry !== void 0);
  const classifierImplementations = Object.values(classifiers ?? {}).filter((entry) => entry !== void 0);
  if (streams.length === 0 && imageImplementations.length === 0 && classifierImplementations.length === 0) {
    throw new Error(`Provider ${input.id}: at least one of "api", "images", or "classifiers" is required.`);
  }
  const baselineModels = input.models;
  let dynamicModels = [];
  const fetchModels = input.fetchModels;
  const currentModels = /* @__PURE__ */ __name(() => {
    const merged = [...baselineModels];
    for (const model2 of dynamicModels) {
      const index = merged.findIndex((entry) => getModelType(entry) === getModelType(model2) && entry.id === model2.id);
      if (index >= 0)
        merged[index] = model2;
      else
        merged.push(model2);
    }
    return merged;
  }, "currentModels");
  const apiFor = /* @__PURE__ */ __name((model2) => single ?? byApi?.[model2.api], "apiFor");
  const dispatch = /* @__PURE__ */ __name((model2, run) => {
    const streams2 = apiFor(model2);
    if (!streams2) {
      return lazyStream(model2, async () => {
        throw new ModelsError("stream", `Provider ${input.id} has no API implementation for "${model2.api}"`);
      });
    }
    return run(streams2);
  }, "dispatch");
  const provider2 = {
    id: input.id,
    name: input.name ?? input.id,
    baseUrl: input.baseUrl,
    headers: input.headers,
    auth: input.auth,
    getModels: /* @__PURE__ */ __name(() => currentModels().filter((model2) => isModelType(model2, "chat")), "getModels"),
    getAllModels: currentModels,
    refreshModels: fetchModels ? async (context) => {
      if (context.stored) {
        const restored = context.stored.models.filter((model2) => model2.provider === input.id).map((model2) => model2);
        if (!await context.publish({
          update: /* @__PURE__ */ __name(() => {
            dynamicModels = restored;
          }, "update")
        })) {
          return;
        }
      }
      if (!context.allowNetwork || context.signal.aborted)
        return;
      const fetched = await fetchModels(context);
      if (context.signal.aborted)
        return;
      const refreshed = fetched.filter(hasKnownModelType);
      await context.publish({
        persist: { models: refreshed, checkedAt: Date.now() },
        update: /* @__PURE__ */ __name(() => {
          dynamicModels = refreshed;
        }, "update")
      });
    } : void 0,
    filterModels: input.filterModels,
    filterAllModels: input.filterAllModels,
    stream: /* @__PURE__ */ __name((model2, context, options) => dispatch(model2, (streams2) => streams2.stream(model2, context, options)), "stream"),
    streamSimple: /* @__PURE__ */ __name((model2, context, options) => dispatch(model2, (streams2) => streams2.streamSimple(model2, context, options)), "streamSimple")
  };
  if (streams.some((entry) => entry.fetchDeferred !== void 0)) {
    provider2.fetchDeferred = (model2, handle, options) => lazyStream(model2, async () => {
      const implementation = apiFor(model2);
      if (!implementation?.fetchDeferred) {
        throw new ModelsError("provider", `Provider ${input.id} does not support deferred responses for "${model2.api}"`);
      }
      return implementation.fetchDeferred(model2, handle, options);
    });
  }
  if (streams.some((entry) => entry.cancelDeferred !== void 0)) {
    provider2.cancelDeferred = async (model2, handle, options) => {
      const implementation = apiFor(model2);
      if (!implementation?.cancelDeferred) {
        throw new ModelsError("provider", `Provider ${input.id} cannot cancel deferred responses for "${model2.api}"`);
      }
      await implementation.cancelDeferred(model2, handle, options);
    };
  }
  if (images && imageImplementations.length > 0) {
    provider2.generateImages = async (model2, context, options) => {
      const implementation = images[model2.api];
      if (!implementation) {
        return imageErrorResult(model2, new ModelsError("provider", `Provider ${input.id} has no image generation implementation for "${model2.api}"`));
      }
      return implementation.generateImages(model2, context, options);
    };
  }
  if (classifiers && classifierImplementations.length > 0) {
    provider2.classify = async (model2, context, options) => {
      const implementation = classifiers[model2.api];
      if (!implementation) {
        return classifierErrorResult(model2, new ModelsError("provider", `Provider ${input.id} has no classifier implementation for "${model2.api}"`));
      }
      return implementation.classify(model2, context, options);
    };
  }
  return provider2;
}
__name(createProvider, "createProvider");
function hasApi(model2, api) {
  return isModelType(model2, "chat") && model2.api === api;
}
__name(hasApi, "hasApi");
function calculateCost(model2, usage) {
  const inputTokens = usage.input + usage.cacheRead + usage.cacheWrite;
  let rates = model2.cost;
  let matchedThreshold = -1;
  for (const tier of model2.cost.tiers ?? []) {
    if (inputTokens > tier.inputTokensAbove && tier.inputTokensAbove > matchedThreshold) {
      rates = tier;
      matchedThreshold = tier.inputTokensAbove;
    }
  }
  const longWrite = usage.cacheWrite1h ?? 0;
  const shortWrite = usage.cacheWrite - longWrite;
  usage.cost.input = rates.input / 1e6 * usage.input;
  usage.cost.output = rates.output / 1e6 * usage.output;
  usage.cost.cacheRead = rates.cacheRead / 1e6 * usage.cacheRead;
  usage.cost.cacheWrite = (rates.cacheWrite * shortWrite + rates.input * 2 * longWrite) / 1e6;
  usage.cost.total = usage.cost.input + usage.cost.output + usage.cost.cacheRead + usage.cost.cacheWrite;
  return usage.cost;
}
__name(calculateCost, "calculateCost");
var EXTENDED_THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
function getSupportedThinkingLevels(model2) {
  if (!model2.reasoning)
    return ["off"];
  return EXTENDED_THINKING_LEVELS.filter((level) => {
    const mapped = model2.thinkingLevelMap?.[level];
    if (mapped === null)
      return false;
    if (level === "xhigh" || level === "max")
      return mapped !== void 0;
    return true;
  });
}
__name(getSupportedThinkingLevels, "getSupportedThinkingLevels");
function clampThinkingLevel(model2, level) {
  const availableLevels = getSupportedThinkingLevels(model2);
  if (availableLevels.includes(level))
    return level;
  const requestedIndex = EXTENDED_THINKING_LEVELS.indexOf(level);
  if (requestedIndex === -1)
    return availableLevels[0] ?? "off";
  for (let i = requestedIndex; i < EXTENDED_THINKING_LEVELS.length; i++) {
    const candidate = EXTENDED_THINKING_LEVELS[i];
    if (availableLevels.includes(candidate))
      return candidate;
  }
  for (let i = requestedIndex - 1; i >= 0; i--) {
    const candidate = EXTENDED_THINKING_LEVELS[i];
    if (availableLevels.includes(candidate))
      return candidate;
  }
  return availableLevels[0] ?? "off";
}
__name(clampThinkingLevel, "clampThinkingLevel");
function modelsAreEqual(a, b) {
  if (!a || !b)
    return false;
  return getModelType(a) === getModelType(b) && a.id === b.id && a.provider === b.provider;
}
__name(modelsAreEqual, "modelsAreEqual");

export {
  defaultProviderAuthContext,
  InMemoryCredentialStore,
  formatThrownValue,
  extractDiagnosticError,
  createAssistantMessageDiagnostic,
  appendAssistantMessageDiagnostic,
  ModelsError,
  InMemoryModelsStore,
  AI_TELEMETRY_SCHEMA,
  PI_AI_VERSION,
  PI_ATTRIBUTION,
  mergeClientIdentity,
  getClientVersion,
  formatUserAgent,
  getAttributionHeaders,
  getOpenCodeSessionHeaders,
  getIdentityHeaders,
  withClientIdentity,
  getModelType,
  isModelType,
  contentText,
  getSystemMessageText,
  renderSystemMessageUpdate,
  createInitialSystemMessage,
  normalizeContext,
  getInitialSystemMessage,
  withoutInitialSystemMessage,
  getCurrentTools,
  getCurrentSystemMessage,
  getCurrentSystemPrompt,
  collapseSystemMessages,
  resolveTranscript,
  toToolDeclaration,
  declarationsEqual,
  getToolStateChanges,
  getDeclaredTools,
  hasToolRedefinitions,
  hasNonAdditiveToolChanges,
  resolveTranscriptTools,
  createModels,
  createProvider,
  hasApi,
  calculateCost,
  getSupportedThinkingLevels,
  clampThinkingLevel,
  modelsAreEqual
};
