import {
  fetchWithRetry,
  getPiUserAgent
} from "./chunk-PNTN457B.js";
import {
  AuthStorage,
  FileAuthStorageBackend,
  getConfigValueEnvVarNames,
  isCommandConfigValue,
  isConfigValueConfigured,
  operationSignal,
  raceWithAbortSignal,
  resolveConfigValueOrThrow,
  resolveHeadersOrThrow
} from "./chunk-H3JRSATQ.js";
import {
  VERSION,
  getAgentDir,
  getFileRevision,
  normalizePath,
  stripBom
} from "./chunk-JGRZB36T.js";
import {
  __name
} from "./chunk-FIOAHQVQ.js";

// pi-dist/pi-coding-agent/core/virtual-models.js
import { isModelType, lazyStream } from "../../pi-ai/sdk-bundle/index.js";
var VIRTUAL_MODEL_API = "pi-virtual";
var VIRTUAL_MODEL_STATE_ENTRY = "pi.virtual-model-state";
var THINKING_LEVELS = ["off", "minimal", "low", "medium", "high", "xhigh", "max"];
function isVirtualModel(model) {
  return model.api === VIRTUAL_MODEL_API;
}
__name(isVirtualModel, "isVirtualModel");
function findLatestResponse(messages) {
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i];
    if (message.role === "assistant" && message.stopReason !== "error" && message.stopReason !== "aborted") {
      return message;
    }
  }
  return void 0;
}
__name(findLatestResponse, "findLatestResponse");
function getBranchSelection(branch, getModel) {
  for (let i = branch.length - 1; i >= 0; i--) {
    const entry = branch[i];
    if (entry.type === "model_change") {
      return { provider: entry.provider, modelId: entry.modelId };
    }
    if (entry.type === "message" && entry.message.role === "assistant" && !isVirtualModel(entry.message)) {
      const response = { provider: entry.message.provider, modelId: entry.message.model };
      const change = findLastModelChange(branch, i);
      const model = change && getModel(change.provider, change.modelId);
      return change && model && isVirtualModel(model) ? change : response;
    }
  }
  return void 0;
}
__name(getBranchSelection, "getBranchSelection");
function findLastModelChange(branch, before) {
  for (let i = before - 1; i >= 0; i--) {
    const entry = branch[i];
    if (entry.type === "model_change")
      return { provider: entry.provider, modelId: entry.modelId };
  }
  return void 0;
}
__name(findLastModelChange, "findLastModelChange");
function getVirtualModelState(branch, provider, modelId) {
  for (let i = branch.length - 1; i >= 0; i--) {
    const entry = branch[i];
    if (entry.type !== "custom" || entry.customType !== VIRTUAL_MODEL_STATE_ENTRY)
      continue;
    const data = entry.data;
    if (data?.provider === provider && data.modelId === modelId)
      return data.state;
  }
  return void 0;
}
__name(getVirtualModelState, "getVirtualModelState");
function createVirtualModel(definition) {
  const levels = definition.thinkingLevels ?? ["off"];
  const thinkingLevelMap = {};
  for (const level of THINKING_LEVELS)
    thinkingLevelMap[level] = levels.includes(level) ? level : null;
  return {
    id: definition.id,
    name: definition.name,
    api: VIRTUAL_MODEL_API,
    provider: definition.provider,
    baseUrl: "",
    reasoning: levels.some((level) => level !== "off"),
    thinkingLevelMap,
    input: definition.input ?? ["text", "image"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: definition.contextWindow ?? 0,
    maxTokens: definition.maxTokens ?? 0
  };
}
__name(createVirtualModel, "createVirtualModel");
function unroutedStream(model) {
  return lazyStream(model, async () => {
    throw new Error(`Virtual model ${model.provider}/${model.id} must be routed before streaming`);
  });
}
__name(unroutedStream, "unroutedStream");
function withVirtualModels(providerId, provider, virtualModels) {
  if (!provider) {
    return {
      id: providerId,
      name: providerId,
      auth: { apiKey: { name: "Virtual model", resolve: /* @__PURE__ */ __name(async () => ({ auth: {}, source: "virtual" }), "resolve") } },
      getModels: /* @__PURE__ */ __name(() => virtualModels, "getModels"),
      stream: unroutedStream,
      streamSimple: unroutedStream
    };
  }
  const ids = new Set(virtualModels.map((model) => model.id));
  const physical = /* @__PURE__ */ __name((models) => models.filter((model) => !isVirtualModel(model) && !(isModelType(model, "chat") && ids.has(model.id))), "physical");
  const virtual = /* @__PURE__ */ __name((models) => models.filter((model) => isVirtualModel(model)), "virtual");
  const { filterModels, filterAllModels } = provider;
  return {
    ...provider,
    getModels: /* @__PURE__ */ __name(() => [...physical(provider.getModels()), ...virtualModels], "getModels"),
    getAllModels: /* @__PURE__ */ __name(() => [...physical(provider.getAllModels?.() ?? provider.getModels()), ...virtualModels], "getAllModels"),
    filterModels: /* @__PURE__ */ __name((models, credential) => {
      const real = physical(models);
      return [...filterModels?.(real, credential) ?? real, ...virtual(models)];
    }, "filterModels"),
    filterAllModels: filterAllModels && ((models, credential) => [...filterAllModels(physical(models), credential), ...virtual(models)]),
    stream: /* @__PURE__ */ __name((model, context, options) => isVirtualModel(model) ? unroutedStream(model) : provider.stream(model, context, options), "stream"),
    streamSimple: /* @__PURE__ */ __name((model, context, options) => isVirtualModel(model) ? unroutedStream(model) : provider.streamSimple(model, context, options), "streamSimple")
  };
}
__name(withVirtualModels, "withVirtualModels");

// pi-dist/pi-coding-agent/core/model-runtime.js
import { dirname, join as join2 } from "node:path";
import { clampThinkingLevel, createModels, getOpenCodeSessionHeaders, lazyStream as lazyStream3, ModelsError, normalizeContext } from "../../pi-ai/sdk-bundle/index.js";
import * as builtinProviderCatalog from "../../pi-ai/sdk-bundle/providers.js";
import { personalizeOpenRouterCatalog } from "../../pi-ai/sdk-bundle/openrouter.js";
import { assertChatModel, assertClassifierInputSupported, assertClassifierModel, assertImageModel, classifierErrorResult as classifierErrorResult2, imageErrorResult as imageErrorResult2 } from "../../pi-ai/utils/model-operations.js";

// pi-dist/pi-coding-agent/core/model-config.js
import { readFile } from "node:fs/promises";
import { Type as Type2 } from "../../../typebox.mjs";
import { Compile } from "../../../typebox-compile.mjs";

// pi-dist/pi-coding-agent/utils/json.js
function stripJsonComments(input) {
  return input.replace(/"(?:\\.|[^"\\])*"|\/\/[^\n]*/g, (m) => m[0] === '"' ? m : "").replace(/"(?:\\.|[^"\\])*"|,(\s*[}\]])/g, (m, tail) => tail ?? (m[0] === '"' ? m : ""));
}
__name(stripJsonComments, "stripJsonComments");

// pi-dist/pi-coding-agent/core/pi-ai-schemas.js
import { ProviderCompatSchema as ProviderCompatJsonSchema } from "../../pi-ai/providers/compat-schema.js";
import { ModelCostSchema as ModelCostJsonSchema, ModelInputLimitsSchema as ModelInputLimitsJsonSchema, ModelInputModalitySchema as ModelInputModalityJsonSchema, ModelPromptCacheSchema as ModelPromptCacheJsonSchema, ModelThinkingLevelSchema as ModelThinkingLevelJsonSchema, ThinkingLevelMapSchema as ThinkingLevelMapJsonSchema } from "../../pi-ai/providers/model-schema.js";
import { Type } from "../../../typebox.mjs";
var ProviderCompatSchema = Type.Unsafe(ProviderCompatJsonSchema);
var ModelCostSchema = Type.Unsafe(ModelCostJsonSchema);
var ModelInputLimitsSchema = Type.Unsafe(ModelInputLimitsJsonSchema);
var ModelInputModalitySchema = Type.Unsafe(ModelInputModalityJsonSchema);
var ModelPromptCacheSchema = Type.Unsafe(ModelPromptCacheJsonSchema);
var ModelThinkingLevelSchema = Type.Unsafe(ModelThinkingLevelJsonSchema);
var ThinkingLevelMapSchema = Type.Unsafe(ThinkingLevelMapJsonSchema);
var PartialModelCostSchema = Type.Unsafe(withoutRequired(ModelCostJsonSchema));
function withoutRequired(schema) {
  const { required: _required, ...rest } = schema;
  return rest;
}
__name(withoutRequired, "withoutRequired");

// pi-dist/pi-coding-agent/core/model-config.js
var SamplingParamsSchema = Type2.Record(Type2.String(), Type2.Unknown());
var SamplingParamsByThinkingLevelSchema = Type2.Object({
  off: Type2.Optional(SamplingParamsSchema),
  minimal: Type2.Optional(SamplingParamsSchema),
  low: Type2.Optional(SamplingParamsSchema),
  medium: Type2.Optional(SamplingParamsSchema),
  high: Type2.Optional(SamplingParamsSchema),
  xhigh: Type2.Optional(SamplingParamsSchema),
  max: Type2.Optional(SamplingParamsSchema)
}, { description: "samplingParams per thinking level, merged over samplingParams." });
var SamplingParamsFieldSchema = Type2.Record(Type2.String(), Type2.Unknown(), {
  description: "Fields merged into every request body. Applies only to OpenAI-compatible APIs."
});
var HeadersSchema = Type2.Record(Type2.String(), Type2.String(), {
  description: "Request headers. Values resolve like apiKey."
});
var ModelFieldDescriptions = {
  name: "Display name, also matched by --model patterns.",
  reasoning: "Whether the model supports thinking.",
  contextWindow: "Context window in tokens.",
  maxTokens: "Maximum output tokens."
};
var ModelDefinitionSchema = Type2.Object({
  id: Type2.String({ minLength: 1, description: "Model ID sent to the API." }),
  name: Type2.Optional(Type2.String({ minLength: 1, description: ModelFieldDescriptions.name })),
  api: Type2.Optional(Type2.String({ minLength: 1, description: "Overrides the provider api." })),
  baseUrl: Type2.Optional(Type2.String({ minLength: 1, description: "Overrides the provider baseUrl." })),
  reasoning: Type2.Optional(Type2.Boolean({ default: false, description: ModelFieldDescriptions.reasoning })),
  thinkingLevelMap: Type2.Optional(ThinkingLevelMapSchema),
  input: Type2.Optional(Type2.Array(ModelInputModalitySchema, { default: ["text"] })),
  inputLimits: Type2.Optional(ModelInputLimitsSchema),
  cost: Type2.Optional(ModelCostSchema),
  promptCache: Type2.Optional(ModelPromptCacheSchema),
  contextWindow: Type2.Optional(Type2.Number({ exclusiveMinimum: 0, default: 128e3, description: ModelFieldDescriptions.contextWindow })),
  maxTokens: Type2.Optional(Type2.Number({ exclusiveMinimum: 0, default: 16384, description: ModelFieldDescriptions.maxTokens })),
  samplingParams: Type2.Optional(SamplingParamsFieldSchema),
  samplingParamsByThinkingLevel: Type2.Optional(SamplingParamsByThinkingLevelSchema),
  headers: Type2.Optional(HeadersSchema),
  compat: Type2.Optional(ProviderCompatSchema)
});
var ModelOverrideSchema = Type2.Object({
  name: Type2.Optional(Type2.String({ minLength: 1, description: ModelFieldDescriptions.name })),
  reasoning: Type2.Optional(Type2.Boolean({ description: ModelFieldDescriptions.reasoning })),
  thinkingLevelMap: Type2.Optional(ThinkingLevelMapSchema),
  input: Type2.Optional(Type2.Array(ModelInputModalitySchema)),
  inputLimits: Type2.Optional(ModelInputLimitsSchema),
  cost: Type2.Optional(PartialModelCostSchema),
  promptCache: Type2.Optional(ModelPromptCacheSchema),
  contextWindow: Type2.Optional(Type2.Number({ exclusiveMinimum: 0, description: ModelFieldDescriptions.contextWindow })),
  maxTokens: Type2.Optional(Type2.Number({ exclusiveMinimum: 0, description: ModelFieldDescriptions.maxTokens })),
  samplingParams: Type2.Optional(SamplingParamsFieldSchema),
  samplingParamsByThinkingLevel: Type2.Optional(SamplingParamsByThinkingLevelSchema),
  headers: Type2.Optional(HeadersSchema),
  compat: Type2.Optional(ProviderCompatSchema)
});
var ProviderConfigSchema = Type2.Object({
  name: Type2.Optional(Type2.String({ minLength: 1, description: "Display name." })),
  baseUrl: Type2.Optional(Type2.String({ minLength: 1, description: "API base URL. Also applies to the provider's built-in models." })),
  apiKey: Type2.Optional(Type2.String({
    minLength: 1,
    description: 'Literal key, "$NAME" environment interpolation, or "!command" output.'
  })),
  api: Type2.Optional(Type2.String({
    minLength: 1,
    description: `API for the provider's models, such as "openai-completions", "openai-responses", "anthropic-messages", or "google-generative-ai".`
  })),
  oauth: Type2.Optional(Type2.Literal("radius", { description: "OAuth login flow. Requires baseUrl." })),
  headers: Type2.Optional(HeadersSchema),
  compat: Type2.Optional(ProviderCompatSchema),
  authHeader: Type2.Optional(Type2.Boolean({ default: false, description: "Whether to send the resolved API key as Authorization: Bearer." })),
  models: Type2.Optional(Type2.Array(ModelDefinitionSchema)),
  modelOverrides: Type2.Optional(Type2.Record(Type2.String(), ModelOverrideSchema))
});
var ModelsConfigProperties = {
  providers: Type2.Record(Type2.String(), ProviderConfigSchema)
};
var ModelsConfigSchema = Type2.Object({
  $schema: Type2.Optional(Type2.String()),
  ...ModelsConfigProperties
});
var validateModelsConfig = Compile(ModelsConfigSchema);
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
function deepFreeze(value) {
  if (typeof value !== "object" || value === null || Object.isFrozen(value))
    return value;
  for (const child of Object.values(value))
    deepFreeze(child);
  return Object.freeze(value);
}
__name(deepFreeze, "deepFreeze");
var ModelConfig = class _ModelConfig {
  static {
    __name(this, "ModelConfig");
  }
  providers;
  error;
  constructor(providers, error) {
    this.providers = providers;
    this.error = error;
  }
  static async load(modelsJsonPath) {
    if (!modelsJsonPath)
      return new _ModelConfig(/* @__PURE__ */ new Map());
    const path = normalizePath(modelsJsonPath);
    let content;
    try {
      content = await readFile(path, "utf-8");
    } catch (error) {
      if (error.code === "ENOENT")
        return new _ModelConfig(/* @__PURE__ */ new Map());
      return new _ModelConfig(/* @__PURE__ */ new Map(), `Failed to load models.json: ${error instanceof Error ? error.message : error}

File: ${path}`);
    }
    let parsed;
    try {
      parsed = JSON.parse(stripJsonComments(stripBom(content)));
    } catch (error) {
      return new _ModelConfig(/* @__PURE__ */ new Map(), `Failed to parse models.json: ${error instanceof Error ? error.message : error}

File: ${path}`);
    }
    if (!validateModelsConfig.Check(parsed)) {
      const errors = validateModelsConfig.Errors(parsed).map((error) => `  - ${formatValidationPath(error)}: ${error.message}`).join("\n") || "Unknown schema error";
      return new _ModelConfig(/* @__PURE__ */ new Map(), `Invalid models.json schema:
${errors}

File: ${path}`);
    }
    const providers = /* @__PURE__ */ new Map();
    for (const [providerId, provider] of Object.entries(parsed.providers)) {
      providers.set(providerId, deepFreeze(structuredClone(provider)));
    }
    return new _ModelConfig(providers);
  }
  getProvider(providerId) {
    return this.providers.get(providerId);
  }
  getProviderIds() {
    return [...this.providers.keys()];
  }
  getError() {
    return this.error;
  }
};

// pi-dist/pi-coding-agent/core/models-store.js
import { join } from "node:path";
var sharedModelsFileReadState;
var InMemoryCodingAgentModelsStore = class {
  static {
    __name(this, "InMemoryCodingAgentModelsStore");
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
var FileModelsStore = class {
  static {
    __name(this, "FileModelsStore");
  }
  storage;
  path;
  readState;
  constructor(path = join(getAgentDir(), "models-store.json")) {
    this.path = normalizePath(path);
    this.storage = new FileAuthStorageBackend(this.path);
    this.readState = sharedModelsFileReadState?.path === this.path ? sharedModelsFileReadState.readState : { data: {} };
    if (!sharedModelsFileReadState) {
      sharedModelsFileReadState = { path: this.path, readState: this.readState };
    }
  }
  parse(content) {
    return content ? JSON.parse(stripBom(content)) : {};
  }
  updateReadState(readState, data, revision) {
    readState.data = data;
    readState.revision = revision;
  }
  reloadFromStorage(readState, options) {
    return this.storage.withLockAsync(async (content) => {
      const data = this.parse(content);
      this.updateReadState(readState, data, getFileRevision(this.path));
      return { result: data };
    }, options);
  }
  async readLatest(readState, options) {
    options?.signal?.throwIfAborted();
    const revision = getFileRevision(this.path);
    if (revision !== void 0 && revision === readState.revision)
      return readState.data;
    if (!readState.reload) {
      const controller = new AbortController();
      const reload2 = {
        controller,
        promise: this.reloadFromStorage(readState, { signal: controller.signal }),
        readers: 0
      };
      readState.reload = reload2;
      void reload2.promise.then(() => {
        if (readState.reload === reload2)
          readState.reload = void 0;
      }, () => {
        if (readState.reload === reload2)
          readState.reload = void 0;
      });
    }
    const reload = readState.reload;
    reload.readers++;
    try {
      return await raceWithAbortSignal(reload.promise, options?.signal);
    } finally {
      reload.readers--;
      if (reload.readers === 0 && readState.reload === reload) {
        readState.reload = void 0;
        reload.controller.abort();
      }
    }
  }
  async read(providerId, options) {
    const entry = (await this.readLatest(this.readState, options))[providerId];
    options?.signal?.throwIfAborted();
    return entry ? structuredClone(entry) : void 0;
  }
  async write(providerId, entry, options) {
    let latest;
    await this.storage.withLockAsync(async (content) => {
      const current = this.parse(content);
      current[providerId] = structuredClone(entry);
      latest = current;
      return { result: void 0, next: JSON.stringify(current, null, 2) };
    }, options);
    if (latest)
      this.updateReadState(this.readState, latest);
  }
  async delete(providerId, options) {
    let latest;
    await this.storage.withLockAsync(async (content) => {
      const current = this.parse(content);
      delete current[providerId];
      latest = current;
      return { result: void 0, next: JSON.stringify(current, null, 2) };
    }, options);
    if (latest)
      this.updateReadState(this.readState, latest);
  }
};

// pi-dist/pi-coding-agent/core/provider-composer.js
import { isModelType as isModelType2, lazyStream as lazyStream2 } from "../../pi-ai/sdk-bundle/index.js";
import { getApiProvider } from "../../pi-ai/sdk-bundle/compat.js";
import { classifierErrorResult, imageErrorResult } from "../../pi-ai/utils/model-operations.js";
function getAllProviderModels(provider) {
  return provider ? provider.getAllModels?.() ?? provider.getModels() : [];
}
__name(getAllProviderModels, "getAllProviderModels");
function mergeCompat(base, override) {
  if (!override)
    return base;
  const merged = { ...base, ...override };
  const baseNested = base;
  const overrideNested = override;
  const mergedNested = merged;
  for (const key of ["openRouterRouting", "vercelGatewayRouting", "chatTemplateKwargs", "chatTemplateArgs"]) {
    const baseValue = baseNested?.[key];
    const overrideValue = overrideNested[key];
    if (typeof baseValue === "object" && baseValue !== null || typeof overrideValue === "object" && overrideValue !== null) {
      mergedNested[key] = { ...baseValue, ...overrideValue };
    }
  }
  return merged;
}
__name(mergeCompat, "mergeCompat");
function mergeInputLimits(base, override) {
  if (!override)
    return base;
  return {
    ...base,
    ...override,
    images: override.images ? {
      ...base?.images,
      ...override.images,
      resize: override.images.resize ? { ...base?.images?.resize, ...override.images.resize } : base?.images?.resize
    } : base?.images
  };
}
__name(mergeInputLimits, "mergeInputLimits");
function mergeSamplingParamsByThinkingLevel(base, override) {
  if (!override)
    return base;
  const merged = { ...base };
  for (const level of ["off", "minimal", "low", "medium", "high", "xhigh", "max"]) {
    const params = override[level];
    if (params)
      merged[level] = { ...base?.[level], ...params };
  }
  return merged;
}
__name(mergeSamplingParamsByThinkingLevel, "mergeSamplingParamsByThinkingLevel");
function applyModelOverride(model, override) {
  return {
    ...model,
    name: override.name ?? model.name,
    reasoning: override.reasoning ?? model.reasoning,
    thinkingLevelMap: override.thinkingLevelMap ? { ...model.thinkingLevelMap, ...override.thinkingLevelMap } : model.thinkingLevelMap,
    input: override.input ?? model.input,
    inputLimits: mergeInputLimits(model.inputLimits, override.inputLimits),
    cost: override.cost ? {
      input: override.cost.input ?? model.cost.input,
      output: override.cost.output ?? model.cost.output,
      cacheRead: override.cost.cacheRead ?? model.cost.cacheRead,
      cacheWrite: override.cost.cacheWrite ?? model.cost.cacheWrite,
      tiers: override.cost.tiers ?? model.cost.tiers
    } : model.cost,
    promptCache: override.promptCache ? { ...model.promptCache, ...override.promptCache } : model.promptCache,
    contextWindow: override.contextWindow ?? model.contextWindow,
    maxTokens: override.maxTokens ?? model.maxTokens,
    samplingParams: override.samplingParams ? { ...model.samplingParams, ...override.samplingParams } : model.samplingParams,
    samplingParamsByThinkingLevel: mergeSamplingParamsByThinkingLevel(model.samplingParamsByThinkingLevel, override.samplingParamsByThinkingLevel),
    compat: mergeCompat(model.compat, override.compat)
  };
}
__name(applyModelOverride, "applyModelOverride");
function modelFromJson(providerId, definition, providerConfig, defaults) {
  const api = definition.api ?? providerConfig.api ?? defaults?.api;
  if (!api) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: no "api" specified. Set at provider or model level.`);
  }
  const baseUrl = definition.baseUrl ?? providerConfig.baseUrl ?? defaults?.baseUrl;
  if (!baseUrl)
    throw new Error(`Provider ${providerId}: "baseUrl" is required when defining custom models.`);
  if (definition.contextWindow !== void 0 && definition.contextWindow <= 0) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: invalid contextWindow`);
  }
  if (definition.maxTokens !== void 0 && definition.maxTokens <= 0) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: invalid maxTokens`);
  }
  return {
    id: definition.id,
    name: definition.name ?? definition.id,
    api,
    provider: providerId,
    baseUrl,
    reasoning: definition.reasoning ?? false,
    thinkingLevelMap: definition.thinkingLevelMap,
    input: definition.input ?? ["text"],
    inputLimits: definition.inputLimits,
    cost: definition.cost ?? { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    promptCache: definition.promptCache,
    contextWindow: definition.contextWindow ?? 128e3,
    maxTokens: definition.maxTokens ?? 16384,
    samplingParams: definition.samplingParams,
    samplingParamsByThinkingLevel: definition.samplingParamsByThinkingLevel,
    headers: void 0,
    compat: mergeCompat(providerConfig.compat, definition.compat)
  };
}
__name(modelFromJson, "modelFromJson");
function findModelDefaults(models, modelId, api) {
  const chatModels = models.filter((model) => isModelType2(model, "chat"));
  return chatModels.find((model) => model.id === modelId) ?? (api ? chatModels.find((model) => model.api === api) : void 0) ?? chatModels.find((model) => model.api === "openai-completions") ?? chatModels[0];
}
__name(findModelDefaults, "findModelDefaults");
function findExtensionModelDefaults(models, definition) {
  const type = definition.type ?? "chat";
  const candidates = models.filter((model) => isModelType2(model, type));
  return candidates.find((model) => model.id === definition.id) ?? (definition.api ? candidates.find((model) => model.api === definition.api) : void 0) ?? (type === "chat" ? candidates.find((model) => model.api === "openai-completions") : void 0) ?? candidates[0];
}
__name(findExtensionModelDefaults, "findExtensionModelDefaults");
function extensionModelFromDefinition(providerId, models, config, definition) {
  const type = definition.type ?? "chat";
  const defaults = findExtensionModelDefaults(models, definition);
  const api = definition.api ?? (type === "chat" ? config.api : void 0) ?? defaults?.api;
  if (!api) {
    throw new Error(`Provider ${providerId}, model ${definition.id}: no "api" specified. Set it at model level${type === "chat" ? " or provider level" : ""}.`);
  }
  const baseUrl = definition.baseUrl ?? config.baseUrl ?? defaults?.baseUrl;
  if (!baseUrl)
    throw new Error(`Provider ${providerId}: "baseUrl" is required when defining custom models.`);
  if (definition.type === "image") {
    return { ...definition, api, provider: providerId, baseUrl, headers: void 0 };
  }
  if (definition.type === "classifier") {
    return { ...definition, api, provider: providerId, baseUrl, headers: void 0 };
  }
  return { ...definition, api, provider: providerId, baseUrl, headers: void 0 };
}
__name(extensionModelFromDefinition, "extensionModelFromDefinition");
function applyModelsJson(providerId, baseModels, config) {
  if (!config)
    return [...baseModels];
  if (config.oauth && !config.baseUrl) {
    throw new Error(`Provider ${providerId}: "baseUrl" is required when "oauth" is set.`);
  }
  const hasOverrides = config.modelOverrides && Object.keys(config.modelOverrides).length > 0;
  if (!config.models?.length && !config.baseUrl && !config.headers && !config.compat && !hasOverrides && !config.apiKey && !config.oauth && config.authHeader === void 0) {
    throw new Error(`Provider ${providerId}: must specify "baseUrl", "headers", "compat", "modelOverrides", or "models".`);
  }
  const models = baseModels.map((model) => {
    const baseUrl = config.oauth === "radius" ? model.baseUrl : config.baseUrl ?? model.baseUrl;
    if (isModelType2(model, "chat"))
      return { ...model, baseUrl, compat: mergeCompat(model.compat, config.compat) };
    if (isModelType2(model, "classifier") && config.compat) {
      return { ...model, baseUrl, compat: { ...model.compat, ...config.compat } };
    }
    return { ...model, baseUrl };
  });
  for (const definition of config.models ?? []) {
    const existingIndex = models.findIndex((model2) => isModelType2(model2, "chat") && model2.id === definition.id);
    const defaults = findModelDefaults(models, definition.id, definition.api ?? config.api);
    const model = modelFromJson(providerId, definition, config, defaults);
    if (existingIndex >= 0)
      models[existingIndex] = model;
    else
      models.push(model);
  }
  return models;
}
__name(applyModelsJson, "applyModelsJson");
function applyExtension(providerId, models, config) {
  if (!config)
    return [...models];
  if (!config.models) {
    return config.baseUrl ? models.map((model) => ({ ...model, baseUrl: config.baseUrl })) : [...models];
  }
  return config.models.map((definition) => extensionModelFromDefinition(providerId, models, config, definition));
}
__name(applyExtension, "applyExtension");
function adaptOAuth(config) {
  return {
    name: config.name,
    isSubscription: config.isSubscription,
    login: /* @__PURE__ */ __name(async (callbacks) => {
      const credential = await config.login({
        onAuth: /* @__PURE__ */ __name((info) => callbacks.notify({ type: "auth_url", ...info }), "onAuth"),
        onDeviceCode: /* @__PURE__ */ __name((info) => callbacks.notify({ type: "device_code", ...info }), "onDeviceCode"),
        onPrompt: /* @__PURE__ */ __name((prompt) => callbacks.prompt({ type: "text", ...prompt }), "onPrompt"),
        onProgress: /* @__PURE__ */ __name((message) => callbacks.notify({ type: "progress", message }), "onProgress"),
        onManualCodeInput: /* @__PURE__ */ __name(() => callbacks.prompt({ type: "manual_code", message: "Paste the authorization code" }), "onManualCodeInput"),
        onSelect: /* @__PURE__ */ __name((prompt) => callbacks.prompt({ type: "select", ...prompt }), "onSelect"),
        signal: callbacks.signal
      });
      return { ...credential, type: "oauth" };
    }, "login"),
    refresh: /* @__PURE__ */ __name(async (credential, signal) => ({ ...await config.refreshToken(credential, signal), type: "oauth" }), "refresh"),
    toAuth: /* @__PURE__ */ __name(async (credential) => ({ apiKey: config.getApiKey(credential) }), "toAuth")
  };
}
__name(adaptOAuth, "adaptOAuth");
function withConfiguredAuth(auth, headers, authHeader) {
  let mergedHeaders = auth.headers || headers ? { ...auth.headers, ...headers } : void 0;
  if (authHeader) {
    if (!auth.apiKey)
      throw new Error("authHeader requires a resolved API key");
    mergedHeaders = { ...mergedHeaders, Authorization: `Bearer ${auth.apiKey}` };
  }
  return { ...auth, headers: mergedHeaders };
}
__name(withConfiguredAuth, "withConfiguredAuth");
function configuredApiKey(config, extension) {
  return extension?.apiKey ?? config?.apiKey;
}
__name(configuredApiKey, "configuredApiKey");
function configuredHeaders(config, extension) {
  if (!config?.headers && !extension?.headers)
    return void 0;
  return { ...config?.headers, ...extension?.headers };
}
__name(configuredHeaders, "configuredHeaders");
async function configContextEnv(values, ctx, explicit) {
  const env = { ...explicit };
  for (const name of new Set(values.flatMap(getConfigValueEnvVarNames))) {
    if (env[name] !== void 0)
      continue;
    const value = await ctx.env(name);
    if (value !== void 0)
      env[name] = value;
  }
  return Object.keys(env).length > 0 ? env : void 0;
}
__name(configContextEnv, "configContextEnv");
function composeApiKeyAuth(providerId, base, config, extension) {
  const inherited = base?.auth.apiKey;
  const rawKey = configuredApiKey(config, extension);
  const oauth = extension?.oauth ?? base?.auth.oauth;
  if (!inherited && rawKey === void 0 && oauth)
    return void 0;
  const rawHeaders = configuredHeaders(config, extension);
  const authHeader = extension?.authHeader ?? config?.authHeader ?? false;
  return {
    name: inherited?.name ?? "API key",
    login: inherited?.login ?? (async (interaction) => ({
      type: "api_key",
      key: await interaction.prompt({ type: "secret", message: "Enter API key" })
    })),
    check: /* @__PURE__ */ __name(async (input) => {
      if (input.credential) {
        if (inherited?.check)
          return inherited.check(input);
        if (input.credential.key)
          return { type: "api_key", source: "stored credential" };
        const resolved2 = await inherited?.resolve(input);
        return resolved2 ? { type: "api_key", source: resolved2.source } : void 0;
      }
      if (rawKey !== void 0) {
        if (isCommandConfigValue(rawKey))
          return { type: "api_key", source: "configured API key" };
        const envNames = getConfigValueEnvVarNames(rawKey);
        for (const name of envNames) {
          if (await input.ctx.env(name) === void 0)
            return void 0;
        }
        return { type: "api_key", source: "configured API key" };
      }
      if (inherited?.check)
        return inherited.check(input);
      const resolved = await inherited?.resolve(input);
      return resolved ? { type: "api_key", source: resolved.source } : void 0;
    }, "check"),
    resolve: /* @__PURE__ */ __name(async (input) => {
      let result;
      if (input.credential) {
        result = inherited ? await inherited.resolve(input) : input.credential.key ? { auth: { apiKey: input.credential.key }, env: input.credential.env, source: "stored credential" } : void 0;
      } else if (rawKey !== void 0) {
        const env = await configContextEnv([rawKey], input.ctx);
        const key = resolveConfigValueOrThrow(rawKey, `API key for provider "${providerId}"`, env);
        result = inherited ? await inherited.resolve({ ...input, credential: { type: "api_key", key } }) : { auth: { apiKey: key }, source: "configured API key" };
      } else {
        result = await inherited?.resolve(input);
      }
      if (!result)
        return void 0;
      const explicitEnv = { ...input.credential?.env ?? {}, ...result.env ?? {} };
      const headerEnv = await configContextEnv(Object.values(rawHeaders ?? {}), input.ctx, explicitEnv);
      const headers = resolveHeadersOrThrow(rawHeaders, `provider "${providerId}"`, headerEnv);
      return { ...result, auth: withConfiguredAuth(result.auth, headers, authHeader) };
    }, "resolve")
  };
}
__name(composeApiKeyAuth, "composeApiKeyAuth");
function composeOAuthAuth(providerId, base, config, extension) {
  const oauth = extension?.oauth ? adaptOAuth(extension.oauth) : base?.auth.oauth;
  if (!oauth)
    return void 0;
  const rawHeaders = configuredHeaders(config, extension);
  const authHeader = extension?.authHeader ?? config?.authHeader ?? false;
  return {
    ...oauth,
    toAuth: /* @__PURE__ */ __name(async (credential) => {
      const auth = await oauth.toAuth(credential);
      const env = credential.env;
      const headers = resolveHeadersOrThrow(rawHeaders, `provider "${providerId}"`, typeof env === "object" && env !== null ? env : void 0);
      return withConfiguredAuth(auth, headers, authHeader);
    }, "toAuth")
  };
}
__name(composeOAuthAuth, "composeOAuthAuth");
function rawModelHeaders(model, config, extension) {
  const chatDefinition = isModelType2(model, "chat") ? config?.models?.find((entry) => entry.id === model.id) : void 0;
  const extensionModel = extension?.models?.find((entry) => (entry.type ?? "chat") === (model.type ?? "chat") && entry.id === model.id);
  const headers = {
    ...isModelType2(model, "chat") ? config?.modelOverrides?.[model.id]?.headers : void 0,
    ...chatDefinition?.headers,
    ...extensionModel?.headers
  };
  return Object.keys(headers).length > 0 ? headers : void 0;
}
__name(rawModelHeaders, "rawModelHeaders");
function validateExtensionProvider(providerId, base, modelsConfig, extension) {
  if (extension.streamSimple && !extension.api) {
    throw new Error(`Provider ${providerId}: "api" is required when registering streamSimple.`);
  }
  applyExtension(providerId, applyModelsJson(providerId, getAllProviderModels(base), modelsConfig), extension);
}
__name(validateExtensionProvider, "validateExtensionProvider");
function composeModelProvider(providerId, base, modelConfig, extension) {
  const config = modelConfig.getProvider(providerId);
  let extensionOAuthCredential;
  let refreshedExtensionModels;
  const currentExtension = /* @__PURE__ */ __name(() => extension && refreshedExtensionModels ? { ...extension, models: refreshedExtensionModels } : extension, "currentExtension");
  const getAllModels = /* @__PURE__ */ __name(() => {
    let models = applyExtension(providerId, applyModelsJson(providerId, getAllProviderModels(base), config), currentExtension());
    if (extensionOAuthCredential && extension?.oauth?.modifyModels) {
      models = [
        ...extension.oauth.modifyModels(models.filter((model) => isModelType2(model, "chat")), extensionOAuthCredential),
        ...models.filter((model) => !isModelType2(model, "chat"))
      ];
    }
    return models.map((model) => {
      const override = config?.modelOverrides?.[model.id];
      return override && isModelType2(model, "chat") ? applyModelOverride(model, override) : model;
    });
  }, "getAllModels");
  getAllModels();
  const apiKey = composeApiKeyAuth(providerId, base, config, extension);
  const oauth = composeOAuthAuth(providerId, base, config, extension);
  if (!apiKey && !oauth)
    throw new Error(`Provider ${providerId}: no authentication method configured.`);
  const supportsBaseApi = /* @__PURE__ */ __name((model) => base?.getModels().some((entry) => entry.api === model.api) ?? false, "supportsBaseApi");
  const streamWith = /* @__PURE__ */ __name((model, context, options, simple) => lazyStream2(model, async () => {
    if (extension?.streamSimple && model.api === extension.api) {
      return extension.streamSimple(model, context, options);
    }
    if (base && supportsBaseApi(model)) {
      return simple ? base.streamSimple(model, context, options) : base.stream(model, context, options);
    }
    const api = getApiProvider(model.api);
    if (!api)
      throw new Error(`No API provider registered for api: ${model.api}`);
    return simple ? api.streamSimple(model, context, options) : api.stream(model, context, options);
  }, void 0, options?.signal), "streamWith");
  const provider = {
    id: providerId,
    name: extension?.name ?? config?.name ?? base?.name ?? extension?.oauth?.name ?? providerId,
    baseUrl: extension?.baseUrl ?? config?.baseUrl ?? base?.baseUrl,
    headers: base?.headers,
    auth: { ...apiKey ? { apiKey } : {}, ...oauth ? { oauth } : {} },
    getModels: /* @__PURE__ */ __name(() => getAllModels().filter((model) => isModelType2(model, "chat")), "getModels"),
    getAllModels,
    refreshModels: base?.refreshModels || extension?.refreshModels || extension?.oauth?.modifyModels ? async (context) => {
      await base?.refreshModels?.(context);
      let refreshed;
      if (extension?.refreshModels)
        refreshed = await extension.refreshModels(context);
      if (context.signal.aborted)
        return;
      const oauthCredential = context.credential?.type === "oauth" ? context.credential : void 0;
      await context.publish({
        update: /* @__PURE__ */ __name(() => {
          if (refreshed) {
            applyExtension(providerId, applyModelsJson(providerId, getAllProviderModels(base), config), {
              ...extension,
              models: refreshed
            });
            refreshedExtensionModels = refreshed;
          }
          extensionOAuthCredential = oauthCredential;
        }, "update")
      });
    } : void 0,
    filterModels: base?.filterModels ? (models, credential) => base.filterModels(models, credential) : void 0,
    filterAllModels: base?.filterAllModels ? (models, credential) => base.filterAllModels(models, credential) : void 0,
    stream: /* @__PURE__ */ __name((model, context, options) => streamWith(model, context, options, false), "stream"),
    streamSimple: /* @__PURE__ */ __name((model, context, options) => streamWith(model, context, options, true), "streamSimple")
  };
  const fetchDeferred = base?.fetchDeferred;
  if (fetchDeferred) {
    provider.fetchDeferred = (model, handle, options) => fetchDeferred(model, handle, options);
  }
  const cancelDeferred = base?.cancelDeferred;
  if (cancelDeferred) {
    provider.cancelDeferred = (model, handle, options) => cancelDeferred(model, handle, options);
  }
  const extensionImages = extension?.images;
  const generateImages = base?.generateImages;
  if (generateImages || Object.keys(extensionImages ?? {}).length > 0) {
    provider.generateImages = (model, context, options) => {
      const implementation = extensionImages?.[model.api];
      if (implementation)
        return implementation.generateImages(model, context, options);
      if (generateImages)
        return generateImages(model, context, options);
      return Promise.resolve(imageErrorResult(model, new Error(`Provider ${providerId} has no image implementation for "${model.api}"`)));
    };
  }
  const extensionClassifiers = extension?.classifiers;
  const classify = base?.classify;
  if (classify || Object.keys(extensionClassifiers ?? {}).length > 0) {
    provider.classify = (model, context, options) => {
      const implementation = extensionClassifiers?.[model.api];
      if (implementation)
        return implementation.classify(model, context, options);
      if (classify)
        return classify(model, context, options);
      return Promise.resolve(classifierErrorResult(model, new Error(`Provider ${providerId} has no classifier implementation for "${model.api}"`)));
    };
  }
  return provider;
}
__name(composeModelProvider, "composeModelProvider");
function resolveConfiguredModelHeaders(model, config, extension, env) {
  return resolveHeadersOrThrow(rawModelHeaders(model, config, extension), `model "${model.provider}/${model.id}"`, env);
}
__name(resolveConfiguredModelHeaders, "resolveConfiguredModelHeaders");
function resolveCompatibilityRequestConfig(model, config, extension) {
  const configured = resolveHeadersOrThrow({ ...configuredHeaders(config, extension), ...rawModelHeaders(model, config, extension) }, `model "${model.provider}/${model.id}"`);
  return {
    headers: model.headers || configured ? { ...model.headers, ...configured } : void 0,
    authHeader: extension?.authHeader ?? config?.authHeader ?? false
  };
}
__name(resolveCompatibilityRequestConfig, "resolveCompatibilityRequestConfig");
function configuredRequestAuthStatus(config, extension) {
  const value = configuredApiKey(config, extension);
  if (value === void 0)
    return void 0;
  if (isCommandConfigValue(value))
    return { configured: true, source: "models_json_command" };
  const names = getConfigValueEnvVarNames(value);
  if (names.length > 0) {
    return isConfigValueConfigured(value) ? { configured: true, source: "environment", label: names.join(", ") } : { configured: false };
  }
  return { configured: true, source: extension?.apiKey !== void 0 ? "fallback" : "models_json_key" };
}
__name(configuredRequestAuthStatus, "configuredRequestAuthStatus");

// pi-dist/pi-coding-agent/core/remote-catalog-provider.js
import { getModelType, isModelType as isModelType3 } from "../../pi-ai/sdk-bundle/index.js";
var DEFAULT_CATALOG_BASE_URL = "https://pi-in-go.dev";
var REMOTE_CATALOG_ATTEMPT_TIMEOUT_MS = 4e3;
var REMOTE_CATALOG_REFRESH_INTERVAL_MS = 4 * 60 * 60 * 1e3;
var REMOTE_CATALOG_MODEL_TYPES = ["chat", "image", "classifier"];
function isSupportedModelType(model) {
  return model.type === void 0 || typeof model.type === "string" && REMOTE_CATALOG_MODEL_TYPES.includes(model.type);
}
__name(isSupportedModelType, "isSupportedModelType");
function mergeModels(baseline, dynamic) {
  const merged = /* @__PURE__ */ new Map();
  for (const model of [...baseline, ...dynamic])
    merged.set(`${getModelType(model)}\0${model.id}`, model);
  return [...merged.values()];
}
__name(mergeModels, "mergeModels");
function parseCatalog(providerId, value) {
  const entries = Array.isArray(value) ? value : typeof value === "object" && value !== null && "models" in value && Array.isArray(value.models) ? value.models : typeof value === "object" && value !== null ? Object.values(value) : void 0;
  if (!entries)
    throw new Error(`Invalid model catalog for provider "${providerId}"`);
  return entries.filter((entry) => typeof entry === "object" && entry !== null && "id" in entry).filter(isSupportedModelType).map((model) => ({ ...model, provider: providerId }));
}
__name(parseCatalog, "parseCatalog");
async function readCatalog(providerId, response) {
  const lastModified = Date.parse(response.headers.get("last-modified") ?? "");
  return {
    models: parseCatalog(providerId, await response.json()),
    lastModified: Number.isNaN(lastModified) ? 0 : lastModified,
    etag: response.headers.get("etag") ?? void 0
  };
}
__name(readCatalog, "readCatalog");
function isNewer(lastModified, localGeneratedAt) {
  return localGeneratedAt === void 0 || lastModified !== void 0 && lastModified > localGeneratedAt;
}
__name(isNewer, "isNewer");
function remoteModels(entry, localGeneratedAt) {
  return entry && isNewer(entry.lastModified, localGeneratedAt) ? entry.models : [];
}
__name(remoteModels, "remoteModels");
function requestCatalog(providerId, catalogBaseUrl, signal, validator) {
  const url = new URL(`/api/models/providers/${encodeURIComponent(providerId)}`, catalogBaseUrl);
  url.searchParams.set("types", REMOTE_CATALOG_MODEL_TYPES.join(","));
  return fetchWithRetry(url, {
    headers: {
      accept: "application/json",
      "User-Agent": getPiUserAgent(VERSION),
      ...validator ? { "if-none-match": validator } : {}
    },
    signal
  }, { attemptTimeoutMs: REMOTE_CATALOG_ATTEMPT_TIMEOUT_MS });
}
__name(requestCatalog, "requestCatalog");
function withRemoteCatalog(provider, catalogBaseUrl = DEFAULT_CATALOG_BASE_URL, localGeneratedAt) {
  let dynamicModels = [];
  return {
    ...provider,
    getModels: /* @__PURE__ */ __name(() => mergeModels(provider.getModels(), dynamicModels.filter((model) => isModelType3(model, "chat"))), "getModels"),
    getAllModels: /* @__PURE__ */ __name(() => mergeModels(provider.getAllModels?.() ?? provider.getModels(), dynamicModels), "getAllModels"),
    refreshModels: /* @__PURE__ */ __name(async (context) => {
      const stored = context.stored;
      const restored = remoteModels(stored, localGeneratedAt).filter((model) => model.provider === provider.id);
      if (!await context.publish({
        update: /* @__PURE__ */ __name(() => {
          dynamicModels = restored;
        }, "update")
      })) {
        return;
      }
      if (!context.allowNetwork || context.signal.aborted)
        return;
      if (!context.force && stored?.checkedAt !== void 0 && stored.lastModified !== void 0 && Date.now() - stored.checkedAt < REMOTE_CATALOG_REFRESH_INTERVAL_MS) {
        return;
      }
      const validator = stored && stored.models.length > 0 ? stored.etag : void 0;
      const response = await requestCatalog(provider.id, catalogBaseUrl, context.signal, validator);
      if (context.signal.aborted)
        return;
      const checkedAt = Date.now();
      if (response.status === 304 && stored) {
        await context.publish({ persist: { ...stored, checkedAt } });
        return;
      }
      if (response.status === 404 || response.status === 501) {
        await context.publish({
          persist: {
            ...stored ?? { models: [] },
            checkedAt,
            lastModified: 0,
            etag: void 0
          }
        });
        return;
      }
      if (!response.ok) {
        await context.publish({ persist: { ...stored ?? { models: [] }, checkedAt } });
        throw new Error(`Model catalog request failed for ${provider.id}: ${response.status}`);
      }
      const catalog = await readCatalog(provider.id, response);
      if (context.signal.aborted)
        return;
      const entry = { ...catalog, checkedAt };
      const published = remoteModels(entry, localGeneratedAt);
      await context.publish({
        persist: entry,
        update: /* @__PURE__ */ __name(() => {
          dynamicModels = published;
        }, "update")
      });
    }, "refreshModels")
  };
}
__name(withRemoteCatalog, "withRemoteCatalog");
function withPersonalizedCatalog(provider, personalize, catalogBaseUrl = DEFAULT_CATALOG_BASE_URL, localGeneratedAt) {
  let personalModels = [];
  const builtinModels = /* @__PURE__ */ __name(() => provider.getAllModels?.() ?? provider.getModels(), "builtinModels");
  const getAllModels = /* @__PURE__ */ __name(() => personalModels.length > 0 ? personalModels : builtinModels(), "getAllModels");
  return {
    ...provider,
    getModels: /* @__PURE__ */ __name(() => getAllModels().filter((model) => isModelType3(model, "chat")), "getModels"),
    getAllModels,
    refreshModels: /* @__PURE__ */ __name(async (context) => {
      const restored = (context.stored?.models ?? []).filter((model) => model.provider === provider.id);
      if (!await context.publish({
        update: /* @__PURE__ */ __name(() => {
          personalModels = restored;
        }, "update")
      })) {
        return;
      }
      if (!context.allowNetwork || context.signal.aborted)
        return;
      const response = await requestCatalog(provider.id, catalogBaseUrl, context.signal);
      if (context.signal.aborted)
        return;
      let remote = [];
      if (response.status !== 404 && response.status !== 501) {
        if (!response.ok)
          throw new Error(`Model catalog request failed for ${provider.id}: ${response.status}`);
        const catalog = await readCatalog(provider.id, response);
        if (isNewer(catalog.lastModified, localGeneratedAt))
          remote = catalog.models;
      }
      const personalized = await personalize(mergeModels(builtinModels(), remote), context);
      if (context.signal.aborted)
        return;
      await context.publish({
        persist: { models: personalized, checkedAt: Date.now() },
        update: /* @__PURE__ */ __name(() => {
          personalModels = personalized;
        }, "update")
      });
    }, "refreshModels")
  };
}
__name(withPersonalizedCatalog, "withPersonalizedCatalog");

// pi-dist/pi-coding-agent/core/runtime-credentials.js
var RuntimeCredentials = class {
  static {
    __name(this, "RuntimeCredentials");
  }
  store;
  overrides = /* @__PURE__ */ new Map();
  /** Modifications not yet written or failed, for example an OAuth refresh. */
  pendingModifications = /* @__PURE__ */ new Set();
  constructor(store) {
    this.store = store;
  }
  setRuntimeApiKey(providerId, apiKey) {
    this.overrides.set(providerId, apiKey);
  }
  removeRuntimeApiKey(providerId) {
    this.overrides.delete(providerId);
  }
  hasRuntimeApiKey(providerId) {
    return this.overrides.has(providerId);
  }
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    const override = this.overrides.get(providerId);
    return override ? { type: "api_key", key: override } : this.store.read(providerId, options);
  }
  async list(options) {
    const entries = new Map((await this.store.list(options)).map((entry) => [entry.providerId, entry]));
    options?.signal?.throwIfAborted();
    for (const providerId of this.overrides.keys()) {
      entries.set(providerId, { providerId, type: "api_key" });
    }
    return [...entries.values()];
  }
  modify(providerId, fn, options) {
    const modification = this.store.modify(providerId, fn, options);
    const pending = modification.then(() => void 0, () => void 0);
    this.pendingModifications.add(pending);
    void pending.finally(() => this.pendingModifications.delete(pending));
    return modification;
  }
  /**
   * Resolves when every modification started so far is written or failed. An OAuth refresh may
   * already have spent the single-use refresh token, so exiting before its result is written would
   * leave only the spent token in auth.json.
   */
  async settled() {
    await Promise.all(this.pendingModifications);
  }
  async delete(providerId, options) {
    options?.signal?.throwIfAborted();
    await this.store.delete(providerId, options);
    this.overrides.delete(providerId);
  }
};

// pi-dist/pi-coding-agent/core/model-runtime.js
var CredentialSynchronizationError = class extends Error {
  static {
    __name(this, "CredentialSynchronizationError");
  }
  providerId;
  operation;
  credential;
  constructor(providerId, operation, credential, options) {
    super(`Credential ${operation} committed for ${providerId}, but local synchronization failed`, options);
    this.name = "CredentialSynchronizationError";
    this.providerId = providerId;
    this.operation = operation;
    this.credential = credential;
  }
};
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
var ModelRuntime = class _ModelRuntime {
  static {
    __name(this, "ModelRuntime");
  }
  models;
  credentials;
  defaultBuiltins;
  builtins = /* @__PURE__ */ new Map();
  nativeExtensionProviders = /* @__PURE__ */ new Map();
  extensionProviders = /* @__PURE__ */ new Map();
  /** Virtual models by provider id, then model id. */
  virtualModels = /* @__PURE__ */ new Map();
  compositionErrors = /* @__PURE__ */ new Map();
  modelsPath;
  modelNetworkEnabled;
  config;
  snapshot = {
    all: [],
    available: [],
    configuredProviders: /* @__PURE__ */ new Set(),
    storedProviders: /* @__PURE__ */ new Set(),
    auth: /* @__PURE__ */ new Map()
  };
  availabilityRefreshSeq = 0;
  availabilityErrorSeq = 0;
  providerAvailabilitySeq = /* @__PURE__ */ new Map();
  availabilityError;
  credentialOperations = /* @__PURE__ */ new Map();
  constructor(credentials, config, modelsPath, modelsStore, providers, modelNetworkEnabled) {
    this.credentials = credentials;
    this.config = config;
    this.modelsPath = modelsPath;
    this.modelNetworkEnabled = modelNetworkEnabled;
    this.defaultBuiltins = new Map(providers.map((provider) => [provider.id, provider]));
    for (const [providerId, provider] of this.defaultBuiltins)
      this.builtins.set(providerId, provider);
    this.models = createModels({ credentials, modelsStore });
    this.rebuildProviders();
  }
  static async create(options = {}) {
    const credentials = new RuntimeCredentials(options.credentials ?? AuthStorage.create(options.authPath));
    const modelsPath = options.modelsPath === null ? void 0 : options.modelsPath ?? join2(getAgentDir(), "models.json");
    const config = await ModelConfig.load(modelsPath);
    const modelsStore = options.modelsStore ?? (modelsPath ? new FileModelsStore(options.modelsStorePath ?? join2(dirname(modelsPath), "models-store.json")) : new InMemoryCodingAgentModelsStore());
    const builtinModelDataGeneratedAt = builtinProviderCatalog.getBuiltinModelDataGeneratedAt();
    const providers = builtinProviderCatalog.builtinProviders().map((provider) => {
      if (provider.id === "radius")
        return provider;
      if (provider.id === "openrouter") {
        return withPersonalizedCatalog(provider, personalizeOpenRouterCatalog, options.catalogBaseUrl, builtinModelDataGeneratedAt);
      }
      return withRemoteCatalog(provider, options.catalogBaseUrl, builtinModelDataGeneratedAt);
    });
    const runtime = new _ModelRuntime(credentials, config, modelsPath, modelsStore, providers, process.env.PI_OFFLINE === void 0);
    runtime.configureRadiusProviders();
    runtime.rebuildProviders();
    const refreshFromNetwork = runtime.modelNetworkEnabled && options.allowModelNetwork === true;
    const controller = refreshFromNetwork && options.modelRefreshTimeoutMs !== void 0 ? new AbortController() : void 0;
    const timeout = controller ? setTimeout(() => controller.abort(), options.modelRefreshTimeoutMs) : void 0;
    const signal = controller ? options.signal ? AbortSignal.any([options.signal, controller.signal]) : controller.signal : options.signal;
    try {
      if (options.refreshOnCreate !== false) {
        await runtime.refresh({ allowNetwork: refreshFromNetwork, signal });
      }
    } finally {
      if (timeout)
        clearTimeout(timeout);
    }
    return runtime;
  }
  configureRadiusProviders() {
    this.builtins.clear();
    for (const [providerId, provider] of this.defaultBuiltins)
      this.builtins.set(providerId, provider);
    for (const providerId of this.config.getProviderIds()) {
      const config = this.config.getProvider(providerId);
      if (config?.oauth !== "radius" || !config.baseUrl)
        continue;
      this.builtins.set(providerId, builtinProviderCatalog.radiusProvider({
        id: providerId,
        name: config.name ?? providerId,
        gateway: config.baseUrl.replace(/\/v1\/?$/u, "")
      }));
    }
  }
  providerIds() {
    return /* @__PURE__ */ new Set([
      ...this.builtins.keys(),
      ...this.nativeExtensionProviders.keys(),
      ...this.config.getProviderIds(),
      ...this.extensionProviders.keys(),
      ...this.virtualModels.keys()
    ]);
  }
  /** Returns the provider without virtual models, or undefined when only virtual models define it. */
  recomposeProvider(providerId) {
    const provider = this.composeProvider(providerId);
    const virtualModels = [...this.virtualModels.get(providerId)?.values() ?? []].map((entry) => entry.model);
    if (virtualModels.length > 0)
      this.models.setProvider(withVirtualModels(providerId, provider, virtualModels));
    else if (provider)
      this.models.setProvider(provider);
    else
      this.models.deleteProvider(providerId);
    return provider;
  }
  /** The provider without virtual models, or undefined when nothing defines it. */
  composeProvider(providerId) {
    const base = this.nativeExtensionProviders.get(providerId) ?? this.builtins.get(providerId);
    const extension = this.extensionProviders.get(providerId);
    if (!this.config.getProvider(providerId) && !extension) {
      this.compositionErrors.delete(providerId);
      return base;
    }
    try {
      const provider = composeModelProvider(providerId, base, this.config, extension);
      this.compositionErrors.delete(providerId);
      return provider;
    } catch (error) {
      this.compositionErrors.set(providerId, error instanceof Error ? error.message : String(error));
      return base;
    }
  }
  rebuildProviders() {
    this.models.clearProviders();
    this.compositionErrors.clear();
    for (const providerId of this.providerIds())
      this.recomposeProvider(providerId);
    this.updateModelSnapshot();
  }
  updateModelSnapshot() {
    const all = [...this.models.getModels()];
    this.snapshot = {
      ...this.snapshot,
      all,
      available: all.filter((model) => this.snapshot.configuredProviders.has(model.provider))
    };
  }
  async runAvailabilityRefresh(seq, errorSeq, signal) {
    const providers = this.models.getProviders();
    const [available, checks, credentials] = await Promise.all([
      this.models.getAvailable(void 0, { signal }),
      Promise.all(providers.map(async (provider) => [
        provider.id,
        await this.models.checkAuth(provider.id, { signal })
      ])),
      this.credentials.list({ signal })
    ]);
    if (seq !== this.availabilityRefreshSeq)
      return;
    const auth = new Map(checks);
    const configuredProviders = new Set(checks.filter((entry) => entry[1] !== void 0).map(([providerId]) => providerId));
    this.snapshot = {
      all: [...this.models.getModels()],
      available: [...available],
      configuredProviders,
      storedProviders: new Set(credentials.map((entry) => entry.providerId)),
      auth
    };
    if (errorSeq === this.availabilityErrorSeq)
      this.availabilityError = void 0;
  }
  queueAvailabilityRefresh(signal) {
    const seq = ++this.availabilityRefreshSeq;
    for (const [providerId, providerSeq] of this.providerAvailabilitySeq) {
      this.providerAvailabilitySeq.set(providerId, providerSeq + 1);
    }
    const errorSeq = ++this.availabilityErrorSeq;
    const effectiveSignal = operationSignal(signal);
    return this.runAvailabilityRefresh(seq, errorSeq, effectiveSignal).catch((error) => {
      if (errorSeq === this.availabilityErrorSeq && !effectiveSignal.aborted) {
        this.availabilityError = error instanceof Error ? error.message : String(error);
      }
      throw error;
    });
  }
  async refreshProviderAvailability(providerId, signal) {
    ++this.availabilityRefreshSeq;
    const providerSeq = (this.providerAvailabilitySeq.get(providerId) ?? 0) + 1;
    this.providerAvailabilitySeq.set(providerId, providerSeq);
    const errorSeq = ++this.availabilityErrorSeq;
    try {
      const [available, auth, credential] = await Promise.all([
        this.models.getAvailable(providerId, { signal }),
        this.models.checkAuth(providerId, { signal }),
        this.credentials.read(providerId, { signal })
      ]);
      signal.throwIfAborted();
      if (this.providerAvailabilitySeq.get(providerId) !== providerSeq)
        return;
      const configuredProviders = new Set(this.snapshot.configuredProviders);
      const storedProviders = new Set(this.snapshot.storedProviders);
      const authByProvider = new Map(this.snapshot.auth);
      if (auth) {
        configuredProviders.add(providerId);
        authByProvider.set(providerId, auth);
      } else {
        configuredProviders.delete(providerId);
        authByProvider.delete(providerId);
      }
      if (credential)
        storedProviders.add(providerId);
      else
        storedProviders.delete(providerId);
      const all = [...this.models.getModels()];
      const availableById = new Map([...this.snapshot.available.filter((model) => model.provider !== providerId), ...available].map((model) => [
        `${model.provider}\0${model.id}`,
        model
      ]));
      this.snapshot = {
        all,
        available: all.flatMap((model) => availableById.get(`${model.provider}\0${model.id}`) ?? []),
        configuredProviders,
        storedProviders,
        auth: authByProvider
      };
      if (errorSeq === this.availabilityErrorSeq)
        this.availabilityError = void 0;
    } catch (error) {
      if (this.providerAvailabilitySeq.get(providerId) === providerSeq && errorSeq === this.availabilityErrorSeq && !signal.aborted) {
        this.availabilityError = error instanceof Error ? error.message : String(error);
      }
      throw error;
    }
  }
  getProviders() {
    return this.models.getProviders();
  }
  getProvider(providerId) {
    return this.models.getProvider(providerId);
  }
  getModels(providerId) {
    return this.models.getModels(providerId);
  }
  getModel(providerId, modelId) {
    return this.models.getModel(providerId, modelId);
  }
  getModelsOfType(type, providerId) {
    return this.models.getModelsOfType(type, providerId);
  }
  getModelOfType(type, providerId, modelId) {
    return this.models.getModelOfType(type, providerId, modelId);
  }
  getAllModels(providerId) {
    return this.models.getAllModels(providerId);
  }
  getAvailableOfType(type, providerId, options) {
    return this.models.getAvailableOfType(type, providerId, options);
  }
  getAllAvailable(providerId, options) {
    return this.models.getAllAvailable(providerId, options);
  }
  async checkAuth(providerId, options) {
    return this.models.checkAuth(providerId, options);
  }
  async getAvailable(providerId, options) {
    if (providerId) {
      const errorSeq = ++this.availabilityErrorSeq;
      try {
        const available = await this.models.getAvailable(providerId, options);
        if (errorSeq === this.availabilityErrorSeq)
          this.availabilityError = void 0;
        return available;
      } catch (error) {
        if (errorSeq === this.availabilityErrorSeq && !options?.signal?.aborted) {
          this.availabilityError = error instanceof Error ? error.message : String(error);
        }
        throw error;
      }
    }
    await this.queueAvailabilityRefresh(options?.signal);
    return this.snapshot.available;
  }
  getAvailableSnapshot() {
    return this.snapshot.available;
  }
  getError() {
    const errors = [];
    const configError = this.config.getError();
    if (configError)
      errors.push(configError);
    for (const [providerId, error] of this.compositionErrors) {
      errors.push(`Provider "${providerId}": ${error}`);
    }
    if (this.availabilityError)
      errors.push(`Availability refresh: ${this.availabilityError}`);
    return errors.length > 0 ? errors.join("\n\n") : void 0;
  }
  getRegisteredProviderConfig(providerId) {
    return this.extensionProviders.get(providerId);
  }
  getRegisteredProviderIds() {
    return [.../* @__PURE__ */ new Set([...this.extensionProviders.keys(), ...this.nativeExtensionProviders.keys()])];
  }
  getRegisteredNativeProvider(providerId) {
    return this.nativeExtensionProviders.get(providerId);
  }
  /** @internal Compatibility fallback for ModelRegistry when provider auth is unconfigured. */
  getCompatibilityRequestConfig(model) {
    return resolveCompatibilityRequestConfig(model, this.config.getProvider(model.provider), this.extensionProviders.get(model.provider));
  }
  isUsingOAuth(providerId) {
    return this.snapshot.auth.get(providerId)?.type === "oauth";
  }
  isUsingSubscription(providerId) {
    return this.isUsingOAuth(providerId) && this.models.getProvider(providerId)?.auth.oauth?.isSubscription === true;
  }
  hasConfiguredAuth(providerId) {
    return this.snapshot.configuredProviders.has(providerId);
  }
  async getAuth(providerOrModel, overrides = {}) {
    if (typeof providerOrModel === "string")
      return this.models.getAuth(providerOrModel, overrides);
    const resolution = await this.models.getAuth(providerOrModel, overrides);
    if (!resolution)
      return void 0;
    const configuredHeaders2 = resolveConfiguredModelHeaders(providerOrModel, this.config.getProvider(providerOrModel.provider), this.extensionProviders.get(providerOrModel.provider), { ...resolution.env ?? {}, ...overrides.env ?? {} });
    return {
      ...resolution,
      auth: {
        ...resolution.auth,
        headers: mergeHeaders(resolution.auth.headers, configuredHeaders2)
      }
    };
  }
  enqueueCredentialOperation(providerId, signal, task) {
    const previous = this.credentialOperations.get(providerId) ?? Promise.resolve();
    let markStarted;
    const started = new Promise((resolve) => {
      markStarted = resolve;
    });
    const operation = (async () => {
      await previous.catch(() => {
      });
      signal.throwIfAborted();
      markStarted?.();
      return task();
    })();
    const tail = operation.catch(() => {
    });
    this.credentialOperations.set(providerId, tail);
    void tail.then(() => {
      if (this.credentialOperations.get(providerId) === tail)
        this.credentialOperations.delete(providerId);
    });
    return raceWithAbortSignal(started, signal).then(() => operation);
  }
  async synchronizeCredentialState(providerId, operation, credential, signal) {
    try {
      signal.throwIfAborted();
      this.recomposeProvider(providerId);
      const compositionError = this.compositionErrors.get(providerId);
      if (compositionError)
        throw new Error(compositionError);
      const result = await this.models.refresh({ allowNetwork: false, providers: [providerId], signal });
      if (result.aborted)
        signal.throwIfAborted();
      const refreshError = result.errors.get(providerId);
      if (refreshError)
        throw refreshError;
      this.updateModelSnapshot();
      await this.refreshProviderAvailability(providerId, signal);
    } catch (cause) {
      throw new CredentialSynchronizationError(providerId, operation, credential, { cause });
    }
  }
  setRuntimeApiKey(providerId, apiKey, options = {}) {
    const signal = operationSignal(options.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      this.credentials.setRuntimeApiKey(providerId, apiKey);
      await this.synchronizeCredentialState(providerId, "setRuntimeApiKey", { type: "api_key", key: apiKey }, signal);
    });
  }
  removeRuntimeApiKey(providerId, options = {}) {
    const signal = operationSignal(options.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      this.credentials.removeRuntimeApiKey(providerId);
      await this.synchronizeCredentialState(providerId, "removeRuntimeApiKey", void 0, signal);
    });
  }
  /**
   * Wait until credential writes already in progress finish, such as an OAuth refresh started by a
   * background catalog refresh. Call before the process exits so a rotated token is not lost.
   */
  settleCredentialWrites() {
    return this.credentials.settled();
  }
  listCredentials(options) {
    return this.credentials.list(options);
  }
  getProviderAuthStatus(providerId) {
    if (this.credentials.hasRuntimeApiKey(providerId))
      return { configured: true, source: "runtime" };
    if (this.snapshot.storedProviders.has(providerId))
      return { configured: true, source: "stored" };
    const configured = configuredRequestAuthStatus(this.config.getProvider(providerId), this.extensionProviders.get(providerId));
    if (configured)
      return configured;
    const check = this.snapshot.auth.get(providerId);
    return check ? { configured: true, source: "environment", label: check.source } : { configured: false };
  }
  async prepareRequest(model, options) {
    const provider = this.models.getProvider(model.provider);
    if (!provider)
      throw new ModelsError("provider", `Unknown provider: ${model.provider}`);
    const resolution = await this.getAuth(model, {
      apiKey: options?.apiKey,
      env: options?.env,
      signal: options?.signal
    });
    if (!resolution)
      throw new ModelsError("auth", `Provider is not configured: ${model.provider}`);
    const { transformHeaders, ...rawProviderOptions } = options ?? {};
    const providerOptions = rawProviderOptions;
    const requestModel = resolution.auth.baseUrl ? { ...model, baseUrl: resolution.auth.baseUrl } : model;
    const sessionId = "sessionId" in providerOptions && typeof providerOptions.sessionId === "string" ? providerOptions.sessionId : void 0;
    let headers = mergeHeaders(mergeHeaders(getOpenCodeSessionHeaders(requestModel, sessionId), resolution.auth.headers), providerOptions.headers);
    if (transformHeaders)
      headers = await transformHeaders(headers ?? {});
    const env = resolution.env || providerOptions.env ? { ...resolution.env ?? {}, ...providerOptions.env ?? {} } : void 0;
    return {
      provider,
      model: requestModel,
      options: {
        ...providerOptions,
        apiKey: providerOptions.apiKey ?? resolution.auth.apiKey,
        headers,
        env
      }
    };
  }
  stream(model, context, options) {
    const transcript = normalizeContext(context);
    return lazyStream3(model, async () => {
      assertChatModel(model);
      const prepared = await this.prepareRequest(model, options);
      return prepared.provider.stream(prepared.model, transcript, prepared.options);
    }, void 0, options?.signal);
  }
  complete(model, context, options) {
    return this.stream(model, context, options).result();
  }
  streamSimple(model, context, options) {
    const transcript = normalizeContext(context);
    if (isVirtualModel(model)) {
      return lazyStream3(model, async () => {
        const route = await this.resolveModel(model, transcript.messages, {
          reason: "direct",
          thinkingLevel: options?.reasoning ?? "off",
          signal: options?.signal
        });
        const { maxTokens: limit } = route.model;
        const maxTokens = options?.maxTokens && limit > 0 ? Math.min(options.maxTokens, limit) : options?.maxTokens;
        const reasoning = route.thinkingLevel === "off" ? void 0 : route.thinkingLevel;
        const { apiKey, headers, env, ...rest } = options ?? {};
        const auth = route.model.provider === model.provider ? { apiKey, headers, env } : {};
        return this.streamSimple(route.model, context, { ...rest, ...auth, maxTokens, reasoning });
      }, void 0, options?.signal);
    }
    return lazyStream3(model, async () => {
      assertChatModel(model);
      const prepared = await this.prepareRequest(model, options);
      return prepared.provider.streamSimple(prepared.model, transcript, prepared.options);
    }, void 0, options?.signal);
  }
  completeSimple(model, context, options) {
    return this.streamSimple(model, context, options).result();
  }
  streamDeferred(model, handle, options) {
    return lazyStream3(model, async () => {
      assertChatModel(model);
      const prepared = await this.prepareRequest(model, options);
      if (!prepared.provider.fetchDeferred) {
        throw new ModelsError("provider", `Provider ${model.provider} does not support deferred responses`);
      }
      return prepared.provider.fetchDeferred(prepared.model, handle, prepared.options);
    }, void 0, options?.signal);
  }
  async fetchDeferred(model, handle, options) {
    return this.streamDeferred(model, handle, options).result();
  }
  async cancelDeferred(model, handle, options) {
    assertChatModel(model);
    const prepared = await this.prepareRequest(model, options);
    if (!prepared.provider.cancelDeferred) {
      throw new ModelsError("provider", `Provider ${model.provider} does not support deferred responses`);
    }
    await prepared.provider.cancelDeferred(prepared.model, handle, prepared.options);
  }
  async generateImages(model, context, options) {
    try {
      assertImageModel(model);
      const prepared = await this.prepareRequest(model, options);
      if (!prepared.provider.generateImages) {
        throw new ModelsError("provider", `Provider ${model.provider} does not support image generation`);
      }
      return await prepared.provider.generateImages(prepared.model, context, prepared.options);
    } catch (error) {
      return imageErrorResult2(model, error, options?.signal?.aborted);
    }
  }
  async classify(model, context, options) {
    try {
      assertClassifierModel(model);
      assertClassifierInputSupported(model, context);
      const prepared = await this.prepareRequest(model, options);
      if (!prepared.provider.classify) {
        throw new ModelsError("provider", `Provider ${model.provider} does not support classification`);
      }
      return await prepared.provider.classify(prepared.model, context, prepared.options);
    } catch (error) {
      return classifierErrorResult2(model, error, options?.signal?.aborted);
    }
  }
  login(providerId, type, interaction, options) {
    const signal = operationSignal(interaction.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      const credential = await this.models.login(providerId, type, { ...interaction, signal }, options);
      await this.synchronizeCredentialState(providerId, "login", credential, signal);
      return credential;
    });
  }
  logout(providerId, options = {}) {
    const signal = operationSignal(options.signal);
    return this.enqueueCredentialOperation(providerId, signal, async () => {
      await this.models.logout(providerId, { signal });
      await this.synchronizeCredentialState(providerId, "logout", void 0, signal);
    });
  }
  async refresh(options = {}) {
    this.config = await ModelConfig.load(this.modelsPath);
    this.configureRadiusProviders();
    if (options.providers) {
      for (const providerId of new Set(options.providers))
        this.recomposeProvider(providerId);
      this.updateModelSnapshot();
    } else {
      this.rebuildProviders();
    }
    const refreshOptions = {
      ...options,
      allowNetwork: options.allowNetwork ?? this.modelNetworkEnabled
    };
    const result = await this.models.refresh(refreshOptions) ?? {
      aborted: refreshOptions.signal?.aborted ?? false,
      errors: /* @__PURE__ */ new Map()
    };
    const errors = new Map(result.errors);
    this.updateModelSnapshot();
    if (options.providers) {
      await Promise.all([...new Set(options.providers)].map(async (providerId) => {
        try {
          await this.refreshProviderAvailability(providerId, operationSignal(options.signal));
        } catch (error) {
          if (!options.signal?.aborted) {
            errors.set(providerId, error instanceof Error ? error : new Error(String(error)));
          }
        }
      }));
    } else {
      try {
        await this.queueAvailabilityRefresh(options.signal);
      } catch {
      }
    }
    return { aborted: result.aborted || (options.signal?.aborted ?? false), errors };
  }
  registerNativeProvider(provider) {
    if (!provider.id.trim())
      throw new Error("Provider id must not be empty.");
    this.extensionProviders.delete(provider.id);
    this.nativeExtensionProviders.set(provider.id, provider);
    this.recomposeProvider(provider.id);
    this.updateModelSnapshot();
    this.markProvisionallyConfigured(provider.id, configuredRequestAuthStatus(this.config.getProvider(provider.id), void 0), provider.auth.oauth && !provider.auth.apiKey ? "oauth" : "api_key");
    void this.refresh({ allowNetwork: false });
  }
  /**
   * Mark a newly registered provider as configured when it has a stored credential or a configured
   * API key. Availability checks run asynchronously, and callers such as initial model selection
   * read the snapshot before they finish. The next availability pass replaces this entry.
   */
  markProvisionallyConfigured(providerId, configuredStatus, type) {
    if (!this.snapshot.storedProviders.has(providerId) && !configuredStatus?.configured)
      return;
    const configuredProviders = new Set(this.snapshot.configuredProviders).add(providerId);
    const auth = new Map(this.snapshot.auth);
    if (!auth.get(providerId))
      auth.set(providerId, { type, source: "configured provider" });
    this.snapshot = {
      ...this.snapshot,
      auth,
      configuredProviders,
      available: this.snapshot.all.filter((model) => configuredProviders.has(model.provider))
    };
  }
  registerProvider(providerId, config) {
    validateExtensionProvider(providerId, this.builtins.get(providerId), this.config.getProvider(providerId), config);
    this.nativeExtensionProviders.delete(providerId);
    const previous = this.extensionProviders.get(providerId);
    const effective = { ...previous };
    for (const [key, value] of Object.entries(config)) {
      if (value !== void 0)
        effective[key] = value;
    }
    this.extensionProviders.set(providerId, effective);
    this.recomposeProvider(providerId);
    this.updateModelSnapshot();
    this.markProvisionallyConfigured(providerId, configuredRequestAuthStatus(this.config.getProvider(providerId), effective), effective.oauth && !effective.apiKey ? "oauth" : "api_key");
    void this.refresh({ allowNetwork: false });
  }
  unregisterProvider(providerId) {
    this.extensionProviders.delete(providerId);
    this.nativeExtensionProviders.delete(providerId);
    this.recomposeProvider(providerId);
    this.updateModelSnapshot();
    void this.refresh({ allowNetwork: false });
  }
  /**
   * Register a virtual model under `definition.provider`, which may also list physical models or
   * several virtual models. Re-registering the same provider and id replaces the virtual model.
   * Throws when the id belongs to a physical model of that provider.
   */
  registerVirtualModel(definition) {
    const { provider: providerId, id } = definition;
    if (!providerId.trim() || !id.trim())
      throw new Error("Virtual model provider and id must not be empty.");
    const existing = this.models.getModel(providerId, id);
    if (existing && !isVirtualModel(existing)) {
      throw new Error(`Virtual model ${providerId}/${id} conflicts with a physical model.`);
    }
    const models = this.virtualModels.get(providerId) ?? /* @__PURE__ */ new Map();
    models.set(id, { model: createVirtualModel(definition), route: /* @__PURE__ */ __name((request) => definition.route(request), "route") });
    this.virtualModels.set(providerId, models);
    if (!this.recomposeProvider(providerId) && !this.snapshot.configuredProviders.has(providerId)) {
      const auth = new Map(this.snapshot.auth).set(providerId, { type: "api_key", source: "virtual" });
      const configuredProviders = new Set(this.snapshot.configuredProviders).add(providerId);
      this.snapshot = { ...this.snapshot, auth, configuredProviders };
    }
    this.updateModelSnapshot();
    void this.refresh({ allowNetwork: false });
  }
  unregisterVirtualModel(providerId, id) {
    const models = this.virtualModels.get(providerId);
    if (!models?.delete(id))
      return;
    if (models.size === 0)
      this.virtualModels.delete(providerId);
    this.recomposeProvider(providerId);
    this.updateModelSnapshot();
    void this.refresh({ allowNetwork: false });
  }
  /**
   * Ask a virtual model's router for the model and thinking level of one request. The router must
   * return a physical catalog model whose provider has credentials; the thinking level is clamped
   * to that model. Throws when routing fails.
   *
   * `previous` reports the latest successful response in `messages`. A retry passes the failed
   * response as `options.failed`; `messages` no longer contains it. `options.state` is the router
   * state stored by the caller, which also stores the returned state.
   */
  async resolveModel(model, messages, options) {
    const name = `Virtual model ${model.provider}/${model.id}`;
    const virtual = this.virtualModels.get(model.provider)?.get(model.id);
    if (!virtual)
      throw new Error(`${name} is not registered.`);
    const { failed, ...request } = options;
    const latest = findLatestResponse(messages);
    const previousModel = latest && this.getPhysicalModel(latest.provider, latest.model);
    const failedModel = failed && this.getPhysicalModel(failed.provider, failed.model);
    const route = await virtual.route({
      ...request,
      model,
      previous: previousModel && { model: previousModel, thinkingLevel: latest?.thinkingLevel },
      failed: failedModel && failed && { model: failedModel, thinkingLevel: failed.thinkingLevel, message: failed },
      messages
    });
    const target = this.getPhysicalModel(route.model.provider, route.model.id);
    const routed = `${name} routed to ${route.model.provider}/${route.model.id}`;
    if (!target)
      throw new Error(`${routed}, which is not a physical model.`);
    if (!this.hasConfiguredAuth(target.provider))
      throw new Error(`${routed}, which has no credentials.`);
    return { model: target, thinkingLevel: clampThinkingLevel(target, route.thinkingLevel), state: route.state };
  }
  /** A catalog chat model that is not virtual. */
  getPhysicalModel(providerId, modelId) {
    const model = this.models.getModel(providerId, modelId);
    return model && !isVirtualModel(model) ? model : void 0;
  }
};

export {
  VIRTUAL_MODEL_STATE_ENTRY,
  isVirtualModel,
  findLatestResponse,
  getBranchSelection,
  getVirtualModelState,
  InMemoryCodingAgentModelsStore,
  CredentialSynchronizationError,
  ModelRuntime
};
