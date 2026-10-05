import {
  AssistantMessageEventStream,
  AssistantMessageFrameEncoder,
  DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
  EventStream,
  StringEnum,
  Type,
  cleanupSessionResources,
  createAssistantMessageEventStream,
  createFauxCore,
  fauxAssistantMessage,
  fauxProvider,
  fauxText,
  fauxThinking,
  fauxToolCall,
  getOverflowPatterns,
  isContextOverflow,
  isRecoverableLength,
  isRetryableAssistantError,
  lazyApi,
  lazyStream,
  parseJsonWithRepair,
  parseStreamingJson,
  reduceAssistantMessageFrames,
  registerSessionResourceCleanup,
  repairJson,
  retryAssistantCall,
  retryDelayMs,
  uuidv7,
  validateToolArguments,
  validateToolCall
} from "./chunk-5YY4OANL.js";
import {
  ANTHROPIC_API_KEY_ENV,
  ANTHROPIC_AUTH_TOKEN_ENV,
  ANTHROPIC_FEDERATION_RULE_ID_ENV,
  ANTHROPIC_IDENTITY_TOKEN_FILE_ENV,
  ANTHROPIC_OAUTH_TOKEN_ENV,
  ANTHROPIC_ORGANIZATION_ID_ENV,
  ANTHROPIC_SERVICE_ACCOUNT_ID_ENV,
  ANTHROPIC_WORKSPACE_ID_ENV,
  IMAGE_MODELS,
  anthropicMessagesApi,
  azureOpenAIResponsesApi,
  builtinModels,
  findEnvKeys,
  getBuiltinModel,
  getBuiltinModels,
  getBuiltinProviders,
  getEnvApiKey,
  googleGenerativeAIApi,
  googleVertexApi,
  mistralConversationsApi,
  openAICodexResponsesApi,
  openAICompletionsApi,
  openAIResponsesApi,
  piMessagesApi
} from "./chunk-6VYI4PEE.js";
import {
  envApiKeyAuth,
  lazyOAuth
} from "./chunk-VLLDVPPZ.js";
import {
  InMemoryCredentialStore,
  InMemoryModelsStore,
  ModelsError,
  appendAssistantMessageDiagnostic,
  calculateCost,
  clampThinkingLevel,
  collapseSystemMessages,
  contentText,
  createAssistantMessageDiagnostic,
  createInitialSystemMessage,
  createModels,
  createProvider,
  declarationsEqual,
  defaultProviderAuthContext,
  extractDiagnosticError,
  formatThrownValue,
  getCurrentSystemMessage,
  getCurrentSystemPrompt,
  getCurrentTools,
  getDeclaredTools,
  getInitialSystemMessage,
  getModelType,
  getSupportedThinkingLevels,
  getSystemMessageText,
  getToolStateChanges,
  hasApi,
  hasNonAdditiveToolChanges,
  hasToolRedefinitions,
  isModelType,
  modelsAreEqual,
  normalizeContext,
  renderSystemMessageUpdate,
  resolveTranscript,
  resolveTranscriptTools,
  toToolDeclaration,
  withoutInitialSystemMessage
} from "./chunk-Y5ITVTK2.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-ai/compat.js
import { setBedrockProviderModule, bedrockConverseStreamApi } from "../api/bedrock-converse-stream.lazy.js";

// pi-dist/pi-ai/image-models.js
var imageModelsByProvider = /* @__PURE__ */ new Map();
for (const [provider, models] of Object.entries(IMAGE_MODELS)) {
  const imageModels = /* @__PURE__ */ new Map();
  for (const model of Object.values(models)) {
    imageModels.set(model.id, model);
  }
  if (imageModels.size > 0)
    imageModelsByProvider.set(provider, imageModels);
}
function getImageModel(provider, modelId) {
  return imageModelsByProvider.get(provider)?.get(modelId);
}
__name(getImageModel, "getImageModel");
function getImageProviders() {
  return Array.from(imageModelsByProvider.keys());
}
__name(getImageProviders, "getImageProviders");
function getImageModels(provider) {
  const models = imageModelsByProvider.get(provider);
  return models ? Array.from(models.values()) : [];
}
__name(getImageModels, "getImageModels");

// pi-dist/pi-ai/images-api-registry.js
var imagesApiProviderRegistry = /* @__PURE__ */ new Map();
function wrapGenerateImages(api, generateImages2) {
  return (model, context, options) => {
    if (model.api !== api) {
      throw new Error(`Mismatched api: ${model.api} expected ${api}`);
    }
    return generateImages2(model, context, options);
  };
}
__name(wrapGenerateImages, "wrapGenerateImages");
function registerImagesApiProvider(provider, sourceId) {
  imagesApiProviderRegistry.set(provider.api, {
    provider: {
      api: provider.api,
      generateImages: wrapGenerateImages(provider.api, provider.generateImages)
    },
    sourceId
  });
}
__name(registerImagesApiProvider, "registerImagesApiProvider");
function getImagesApiProvider(api) {
  return imagesApiProviderRegistry.get(api)?.provider;
}
__name(getImagesApiProvider, "getImagesApiProvider");

// pi-dist/pi-ai/providers/images/register-builtins.js
var openRouterImagesProviderModulePromise;
function createLazyLoadErrorImages(model, error) {
  return {
    api: model.api,
    provider: model.provider,
    model: model.id,
    output: [],
    stopReason: "error",
    errorMessage: error instanceof Error ? error.message : String(error),
    timestamp: Date.now()
  };
}
__name(createLazyLoadErrorImages, "createLazyLoadErrorImages");
function loadOpenRouterImagesProviderModule() {
  openRouterImagesProviderModulePromise ||= import("./chunk-U2B7BJMX.js").then((module) => module);
  return openRouterImagesProviderModulePromise;
}
__name(loadOpenRouterImagesProviderModule, "loadOpenRouterImagesProviderModule");
var generateImagesOpenRouter = /* @__PURE__ */ __name(async (model, context, options) => {
  try {
    const module = await loadOpenRouterImagesProviderModule();
    return await module.generateImages(model, context, options);
  } catch (error) {
    return createLazyLoadErrorImages(model, error);
  }
}, "generateImagesOpenRouter");
function registerBuiltInImagesApiProviders() {
  registerImagesApiProvider({
    api: "openrouter-images",
    generateImages: generateImagesOpenRouter
  });
}
__name(registerBuiltInImagesApiProviders, "registerBuiltInImagesApiProviders");
registerBuiltInImagesApiProviders();

// pi-dist/pi-ai/images.js
function resolveImagesApiProvider(api) {
  const provider = getImagesApiProvider(api);
  if (!provider) {
    throw new Error(`No API provider registered for api: ${api}`);
  }
  return provider;
}
__name(resolveImagesApiProvider, "resolveImagesApiProvider");
async function generateImages(model, context, options) {
  const provider = resolveImagesApiProvider(model.api);
  return provider.generateImages(model, context, options);
}
__name(generateImages, "generateImages");

// pi-dist/pi-ai/legacy-api-aliases.js
var anthropicMessagesStreams = anthropicMessagesApi();
var azureOpenAIResponsesStreams = azureOpenAIResponsesApi();
var googleGenerativeAIStreams = googleGenerativeAIApi();
var googleVertexStreams = googleVertexApi();
var mistralConversationsStreams = mistralConversationsApi();
var openAICodexResponsesStreams = openAICodexResponsesApi();
var openAICompletionsStreams = openAICompletionsApi();
var openAIResponsesStreams = openAIResponsesApi();
var streamAnthropic = anthropicMessagesStreams.stream;
var streamSimpleAnthropic = anthropicMessagesStreams.streamSimple;
var streamAzureOpenAIResponses = azureOpenAIResponsesStreams.stream;
var streamSimpleAzureOpenAIResponses = azureOpenAIResponsesStreams.streamSimple;
var streamGoogle = googleGenerativeAIStreams.stream;
var streamSimpleGoogle = googleGenerativeAIStreams.streamSimple;
var streamGoogleVertex = googleVertexStreams.stream;
var streamSimpleGoogleVertex = googleVertexStreams.streamSimple;
var streamMistral = mistralConversationsStreams.stream;
var streamSimpleMistral = mistralConversationsStreams.streamSimple;
var streamOpenAICodexResponses = openAICodexResponsesStreams.stream;
var streamSimpleOpenAICodexResponses = openAICodexResponsesStreams.streamSimple;
var streamOpenAICompletions = openAICompletionsStreams.stream;
var streamSimpleOpenAICompletions = openAICompletionsStreams.streamSimple;
var streamOpenAIResponses = openAIResponsesStreams.stream;
var streamSimpleOpenAIResponses = openAIResponsesStreams.streamSimple;

// pi-dist/pi-ai/compat.js
import { bedrockConverseStreamApi as bedrockConverseStreamApi2 } from "../api/bedrock-converse-stream.lazy.js";
var getModel = getBuiltinModel;
var getModels = getBuiltinModels;
var getProviders = getBuiltinProviders;
var apiProviderRegistry = /* @__PURE__ */ new Map();
function wrapStream(api, stream2) {
  return (model, context, options) => {
    if (model.api !== api) {
      throw new Error(`Mismatched api: ${model.api} expected ${api}`);
    }
    return stream2(model, context, options);
  };
}
__name(wrapStream, "wrapStream");
function wrapStreamSimple(api, streamSimple2) {
  return (model, context, options) => {
    if (model.api !== api) {
      throw new Error(`Mismatched api: ${model.api} expected ${api}`);
    }
    return streamSimple2(model, context, options);
  };
}
__name(wrapStreamSimple, "wrapStreamSimple");
function registerApiProvider(provider, sourceId) {
  apiProviderRegistry.set(provider.api, {
    provider: {
      api: provider.api,
      stream: wrapStream(provider.api, provider.stream),
      streamSimple: wrapStreamSimple(provider.api, provider.streamSimple)
    },
    sourceId
  });
}
__name(registerApiProvider, "registerApiProvider");
function getApiProvider(api) {
  return apiProviderRegistry.get(api)?.provider;
}
__name(getApiProvider, "getApiProvider");
function getApiProviders() {
  return Array.from(apiProviderRegistry.values(), (entry) => entry.provider);
}
__name(getApiProviders, "getApiProviders");
function unregisterApiProviders(sourceId) {
  for (const [api, entry] of apiProviderRegistry.entries()) {
    if (entry.sourceId === sourceId) {
      apiProviderRegistry.delete(api);
    }
  }
}
__name(unregisterApiProviders, "unregisterApiProviders");
function clearApiProviders() {
  apiProviderRegistry.clear();
}
__name(clearApiProviders, "clearApiProviders");
function registerFauxProvider(options = {}) {
  const core = createFauxCore(options);
  const sourceId = `faux-provider-${Math.random().toString(36).slice(2, 10)}`;
  registerApiProvider({ api: core.api, stream: core.stream, streamSimple: core.streamSimple }, sourceId);
  return {
    api: core.api,
    models: core.models,
    getModel: core.getModel,
    state: core.state,
    setResponses: core.setResponses,
    appendResponses: core.appendResponses,
    getPendingResponseCount: core.getPendingResponseCount,
    unregister() {
      unregisterApiProviders(sourceId);
    }
  };
}
__name(registerFauxProvider, "registerFauxProvider");
var BUILTIN_APIS = [
  ["anthropic-messages", anthropicMessagesApi()],
  ["openai-completions", openAICompletionsApi()],
  ["openai-responses", openAIResponsesApi()],
  ["openai-codex-responses", openAICodexResponsesApi()],
  ["azure-openai-responses", azureOpenAIResponsesApi()],
  ["google-generative-ai", googleGenerativeAIApi()],
  ["google-vertex", googleVertexApi()],
  ["mistral-conversations", mistralConversationsApi()],
  ["bedrock-converse-stream", bedrockConverseStreamApi2()],
  ["pi-messages", piMessagesApi()]
];
var builtinApiProviderInstances = /* @__PURE__ */ new Map();
function registerBuiltInApiProviders() {
  for (const [api, streams] of BUILTIN_APIS) {
    if (!getApiProvider(api)) {
      registerApiProvider({ api, stream: streams.stream, streamSimple: streams.streamSimple });
    }
    builtinApiProviderInstances.set(api, getApiProvider(api));
  }
}
__name(registerBuiltInApiProviders, "registerBuiltInApiProviders");
function resetApiProviders() {
  clearApiProviders();
  builtinApiProviderInstances.clear();
  registerBuiltInApiProviders();
}
__name(resetApiProviders, "resetApiProviders");
registerBuiltInApiProviders();
var compatModels = builtinModels();
var AMBIENT_AUTH_MARKER = "<authenticated>";
function hasExplicitApiKey(apiKey) {
  return typeof apiKey === "string" && apiKey.trim().length > 0;
}
__name(hasExplicitApiKey, "hasExplicitApiKey");
function withEnvApiKey(model, options) {
  if (hasExplicitApiKey(options?.apiKey))
    return options;
  const apiKey = getEnvApiKey(model.provider, options?.env);
  if (!apiKey || apiKey === AMBIENT_AUTH_MARKER)
    return options;
  return { ...options, apiKey };
}
__name(withEnvApiKey, "withEnvApiKey");
function hasResolvedCloudflareAuth(options) {
  return hasExplicitApiKey(options?.apiKey) || typeof options?.headers?.["cf-aig-authorization"] === "string";
}
__name(hasResolvedCloudflareAuth, "hasResolvedCloudflareAuth");
function getBuiltinProviderForModel(model) {
  if (getApiProvider(model.api) !== builtinApiProviderInstances.get(model.api))
    return void 0;
  const provider = compatModels.getProvider(model.provider);
  return provider?.getModels().some((candidate) => candidate.api === model.api) ? provider : void 0;
}
__name(getBuiltinProviderForModel, "getBuiltinProviderForModel");
function resolveApiProvider(api) {
  const provider = getApiProvider(api);
  if (!provider) {
    throw new Error(`No API provider registered for api: ${api}`);
  }
  return provider;
}
__name(resolveApiProvider, "resolveApiProvider");
function stream(model, context, options) {
  const transcript = normalizeContext(context);
  const builtinProvider = getBuiltinProviderForModel(model);
  if (builtinProvider) {
    if (model.provider.startsWith("cloudflare-") && !hasResolvedCloudflareAuth(options)) {
      return compatModels.stream(model, transcript, options);
    }
    return builtinProvider.stream(model, transcript, withEnvApiKey(model, options));
  }
  const provider = resolveApiProvider(model.api);
  return provider.stream(model, transcript, withEnvApiKey(model, options));
}
__name(stream, "stream");
async function complete(model, context, options) {
  const s = stream(model, context, options);
  return s.result();
}
__name(complete, "complete");
function streamSimple(model, context, options) {
  const transcript = normalizeContext(context);
  const builtinProvider = getBuiltinProviderForModel(model);
  if (builtinProvider) {
    if (model.provider.startsWith("cloudflare-") && !hasResolvedCloudflareAuth(options)) {
      return compatModels.streamSimple(model, transcript, options);
    }
    return builtinProvider.streamSimple(model, transcript, withEnvApiKey(model, options));
  }
  const provider = resolveApiProvider(model.api);
  return provider.streamSimple(model, transcript, withEnvApiKey(model, options));
}
__name(streamSimple, "streamSimple");
async function completeSimple(model, context, options) {
  const s = streamSimple(model, context, options);
  return s.result();
}
__name(completeSimple, "completeSimple");
export {
  ANTHROPIC_API_KEY_ENV,
  ANTHROPIC_AUTH_TOKEN_ENV,
  ANTHROPIC_FEDERATION_RULE_ID_ENV,
  ANTHROPIC_IDENTITY_TOKEN_FILE_ENV,
  ANTHROPIC_OAUTH_TOKEN_ENV,
  ANTHROPIC_ORGANIZATION_ID_ENV,
  ANTHROPIC_SERVICE_ACCOUNT_ID_ENV,
  ANTHROPIC_WORKSPACE_ID_ENV,
  AssistantMessageEventStream,
  AssistantMessageFrameEncoder,
  DEFAULT_MAX_AGENT_RETRY_DELAY_MS,
  EventStream,
  InMemoryCredentialStore,
  InMemoryModelsStore,
  ModelsError,
  StringEnum,
  Type,
  anthropicMessagesApi,
  appendAssistantMessageDiagnostic,
  azureOpenAIResponsesApi,
  bedrockConverseStreamApi,
  calculateCost,
  clampThinkingLevel,
  cleanupSessionResources,
  collapseSystemMessages,
  complete,
  completeSimple,
  contentText,
  createAssistantMessageDiagnostic,
  createAssistantMessageEventStream,
  createFauxCore,
  createInitialSystemMessage,
  createModels,
  createProvider,
  declarationsEqual,
  defaultProviderAuthContext,
  envApiKeyAuth,
  extractDiagnosticError,
  fauxAssistantMessage,
  fauxProvider,
  fauxText,
  fauxThinking,
  fauxToolCall,
  findEnvKeys,
  formatThrownValue,
  generateImages,
  generateImagesOpenRouter,
  getApiProvider,
  getApiProviders,
  getCurrentSystemMessage,
  getCurrentSystemPrompt,
  getCurrentTools,
  getDeclaredTools,
  getEnvApiKey,
  getImageModel,
  getImageModels,
  getImageProviders,
  getImagesApiProvider,
  getInitialSystemMessage,
  getModel,
  getModelType,
  getModels,
  getOverflowPatterns,
  getProviders,
  getSupportedThinkingLevels,
  getSystemMessageText,
  getToolStateChanges,
  googleGenerativeAIApi,
  googleVertexApi,
  hasApi,
  hasNonAdditiveToolChanges,
  hasToolRedefinitions,
  isContextOverflow,
  isModelType,
  isRecoverableLength,
  isRetryableAssistantError,
  lazyApi,
  lazyOAuth,
  lazyStream,
  mistralConversationsApi,
  modelsAreEqual,
  normalizeContext,
  openAICodexResponsesApi,
  openAICompletionsApi,
  openAIResponsesApi,
  parseJsonWithRepair,
  parseStreamingJson,
  piMessagesApi,
  reduceAssistantMessageFrames,
  registerApiProvider,
  registerBuiltInApiProviders,
  registerBuiltInImagesApiProviders,
  registerFauxProvider,
  registerImagesApiProvider,
  registerSessionResourceCleanup,
  renderSystemMessageUpdate,
  repairJson,
  resetApiProviders,
  resolveTranscript,
  resolveTranscriptTools,
  retryAssistantCall,
  retryDelayMs,
  setBedrockProviderModule,
  stream,
  streamAnthropic,
  streamAzureOpenAIResponses,
  streamGoogle,
  streamGoogleVertex,
  streamMistral,
  streamOpenAICodexResponses,
  streamOpenAICompletions,
  streamOpenAIResponses,
  streamSimple,
  streamSimpleAnthropic,
  streamSimpleAzureOpenAIResponses,
  streamSimpleGoogle,
  streamSimpleGoogleVertex,
  streamSimpleMistral,
  streamSimpleOpenAICodexResponses,
  streamSimpleOpenAICompletions,
  streamSimpleOpenAIResponses,
  toToolDeclaration,
  unregisterApiProviders,
  uuidv7,
  validateToolArguments,
  validateToolCall,
  withoutInitialSystemMessage
};
