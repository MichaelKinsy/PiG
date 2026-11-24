import {
  __name
} from "./chunk-SHUYVCID.js";

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
function ownedSlice(text, start, end = text.length) {
  return `${text.slice(start, end)} `.slice(0, -1);
}
__name(ownedSlice, "ownedSlice");

// pi-dist/pi-ai/utils/version.js
var PI_AI_VERSION = "1.6.0";

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
    getModels: /* @__PURE__ */ __name((provider) => models.getModels(provider), "getModels"),
    getModel: /* @__PURE__ */ __name((provider, id) => models.getModel(provider, id), "getModel"),
    getModelsOfType: /* @__PURE__ */ __name((type, provider) => models.getModelsOfType(type, provider), "getModelsOfType"),
    getModelOfType: /* @__PURE__ */ __name((type, provider, id) => models.getModelOfType(type, provider, id), "getModelOfType"),
    getAllModels: /* @__PURE__ */ __name((provider) => models.getAllModels(provider), "getAllModels"),
    refresh: /* @__PURE__ */ __name((options) => models.refresh(options), "refresh"),
    checkAuth: /* @__PURE__ */ __name((providerId, options) => models.checkAuth(providerId, options), "checkAuth"),
    getAvailable: /* @__PURE__ */ __name((providerId, options) => models.getAvailable(providerId, options), "getAvailable"),
    getAvailableOfType: /* @__PURE__ */ __name((type, providerId, options) => models.getAvailableOfType(type, providerId, options), "getAvailableOfType"),
    getAllAvailable: /* @__PURE__ */ __name((providerId, options) => models.getAllAvailable(providerId, options), "getAllAvailable"),
    getAuth: models.getAuth.bind(models),
    login: /* @__PURE__ */ __name((providerId, type, interaction, options) => models.login(providerId, type, interaction, options), "login"),
    logout: /* @__PURE__ */ __name((providerId, options) => models.logout(providerId, options), "logout"),
    stream: /* @__PURE__ */ __name((model, context, options) => models.stream(model, context, withIdentity(options)), "stream"),
    complete: /* @__PURE__ */ __name((model, context, options) => models.complete(model, context, withIdentity(options)), "complete"),
    streamSimple: /* @__PURE__ */ __name((model, context, options) => models.streamSimple(model, context, withIdentity(options)), "streamSimple"),
    completeSimple: /* @__PURE__ */ __name((model, context, options) => models.completeSimple(model, context, withIdentity(options)), "completeSimple"),
    streamDeferred: /* @__PURE__ */ __name((model, handle, options) => models.streamDeferred(model, handle, withIdentity(options)), "streamDeferred"),
    fetchDeferred: /* @__PURE__ */ __name((model, handle, options) => models.fetchDeferred(model, handle, withIdentity(options)), "fetchDeferred"),
    cancelDeferred: /* @__PURE__ */ __name((model, handle, options) => models.cancelDeferred(model, handle, withIdentity(options)), "cancelDeferred"),
    generateImages: /* @__PURE__ */ __name((model, context, options) => models.generateImages(model, context, withIdentity(options)), "generateImages"),
    classify: /* @__PURE__ */ __name((model, context, options) => models.classify(model, context, withIdentity(options)), "classify")
  };
  if (!isMutableModels(models))
    return wrapped;
  const mutable = {
    ...wrapped,
    setProvider: /* @__PURE__ */ __name((provider) => models.setProvider(provider), "setProvider"),
    deleteProvider: /* @__PURE__ */ __name((id) => models.deleteProvider(id), "deleteProvider"),
    clearProviders: /* @__PURE__ */ __name(() => models.clearProviders(), "clearProviders")
  };
  return mutable;
}
__name(withClientIdentity, "withClientIdentity");

export {
  formatThrownValue,
  extractDiagnosticError,
  createAssistantMessageDiagnostic,
  appendAssistantMessageDiagnostic,
  contentText,
  getSystemMessageText,
  renderSystemMessageUpdate,
  ownedSlice,
  PI_AI_VERSION,
  PI_ATTRIBUTION,
  mergeClientIdentity,
  getClientVersion,
  formatUserAgent,
  getAttributionHeaders,
  getOpenCodeSessionHeaders,
  getIdentityHeaders,
  withClientIdentity
};
