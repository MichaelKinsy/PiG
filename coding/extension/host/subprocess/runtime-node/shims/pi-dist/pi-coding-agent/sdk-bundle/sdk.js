import {
  AgentSession,
  AgentSessionRuntime,
  CacheWarmer,
  DEFAULT_THINKING_LEVEL,
  DEFAULT_TOOL_NAMES,
  DefaultResourceLoader,
  ModelRuntime,
  SessionImportFileNotFoundError,
  SessionManager,
  SettingsManager,
  convertToLlm,
  createAgentSessionFromServices,
  createAgentSessionRuntime,
  createAgentSessionServices,
  findInitialModel,
  formatNoModelsAvailableMessage,
  getBranchSelection,
  getDefaultSessionDir,
  isInstallTelemetryEnabled,
  time
} from "./chunk-FZUYDG7R.js";
import {
  createBashTool,
  createCodingTools,
  createEditTool,
  createFindTool,
  createGrepTool,
  createLsTool,
  createPowerShellTool,
  createReadOnlyTools,
  createReadTool,
  createWriteTool,
  withFileMutationQueue
} from "./chunk-2YBZ3URD.js";
import "./chunk-M5LAR3ND.js";
import "./chunk-RUCWNNX6.js";
import "./chunk-VSG4A7HV.js";
import "./chunk-YJMZRBMJ.js";
import "./chunk-65Z52CAH.js";
import {
  getAgentDir,
  resolvePath
} from "./chunk-6PEVBP2X.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/sdk.js
import { join } from "node:path";
import { Agent, setDefaultStreamFn } from "../../pi-agent-core/index.js";
import { clampThinkingLevel, streamSimple } from "../../pi-ai/sdk-bundle/compat.js";

// pi-dist/pi-coding-agent/core/provider-attribution.js
var OPENROUTER_HOST = "openrouter.ai";
var NVIDIA_NIM_HOST = "integrate.api.nvidia.com";
var CLOUDFLARE_API_HOST = "api.cloudflare.com";
var CLOUDFLARE_AI_GATEWAY_HOST = "gateway.ai.cloudflare.com";
var OPENCODE_HOST = "opencode.ai";
function matchesHost(baseUrl, expectedHost) {
  try {
    return new URL(baseUrl).hostname === expectedHost;
  } catch {
    return false;
  }
}
__name(matchesHost, "matchesHost");
function isOpenRouterModel(model) {
  return model.provider === "openrouter" || model.baseUrl.includes(OPENROUTER_HOST);
}
__name(isOpenRouterModel, "isOpenRouterModel");
function isNvidiaNimModel(model) {
  return model.provider === "nvidia" || matchesHost(model.baseUrl, NVIDIA_NIM_HOST);
}
__name(isNvidiaNimModel, "isNvidiaNimModel");
function isCloudflareModel(model) {
  return model.provider === "cloudflare-workers-ai" || model.provider === "cloudflare-ai-gateway" || matchesHost(model.baseUrl, CLOUDFLARE_API_HOST) || matchesHost(model.baseUrl, CLOUDFLARE_AI_GATEWAY_HOST);
}
__name(isCloudflareModel, "isCloudflareModel");
function getDefaultAttributionHeaders(model, settingsManager) {
  if (!isInstallTelemetryEnabled(settingsManager)) {
    return void 0;
  }
  if (isOpenRouterModel(model)) {
    return {
      "HTTP-Referer": "https://github.com/MichaelKinsy/PiG",
      "X-OpenRouter-Title": "PiG",
      "X-OpenRouter-Categories": "cli-agent"
    };
  }
  if (isNvidiaNimModel(model)) {
    return {
      "X-BILLING-INVOKE-ORIGIN": "PiG"
    };
  }
  if (isCloudflareModel(model)) {
    return {
      "User-Agent": "pig-coding-agent"
    };
  }
  return void 0;
}
__name(getDefaultAttributionHeaders, "getDefaultAttributionHeaders");
function getSessionHeaders(model, sessionId) {
  if (!sessionId)
    return void 0;
  if (model.provider !== "opencode" && model.provider !== "opencode-go" && !matchesHost(model.baseUrl, OPENCODE_HOST)) {
    return void 0;
  }
  return { "x-opencode-session": sessionId, "x-opencode-client": "pig" };
}
__name(getSessionHeaders, "getSessionHeaders");
function mergeProviderAttributionHeaders(model, settingsManager, sessionId, ...headerSources) {
  const merged = {
    ...getSessionHeaders(model, sessionId),
    ...getDefaultAttributionHeaders(model, settingsManager)
  };
  for (const headers of headerSources) {
    if (headers) {
      Object.assign(merged, headers);
    }
  }
  return Object.keys(merged).length > 0 ? merged : void 0;
}
__name(mergeProviderAttributionHeaders, "mergeProviderAttributionHeaders");

// pi-dist/pi-coding-agent/core/sdk.js
setDefaultStreamFn(streamSimple);
function getDefaultAgentDir() {
  return getAgentDir();
}
__name(getDefaultAgentDir, "getDefaultAgentDir");
async function createAgentSession(options = {}) {
  const cwd = resolvePath(options.cwd ?? options.sessionManager?.getCwd() ?? process.cwd());
  const agentDir = options.agentDir ? resolvePath(options.agentDir) : getDefaultAgentDir();
  let resourceLoader = options.resourceLoader;
  const authPath = options.agentDir ? join(agentDir, "auth.json") : void 0;
  const modelsPath = options.agentDir ? join(agentDir, "models.json") : void 0;
  const modelRuntime = options.modelRuntime ?? await ModelRuntime.create({ authPath, modelsPath });
  const settingsManager = options.settingsManager ?? SettingsManager.create(cwd, agentDir);
  const sessionManager = options.sessionManager ?? SessionManager.create(cwd, getDefaultSessionDir(cwd, agentDir));
  if (!resourceLoader) {
    resourceLoader = new DefaultResourceLoader({ cwd, agentDir, settingsManager });
    await resourceLoader.reload();
    time("resourceLoader.reload");
  }
  const existingSession = sessionManager.buildSessionContext();
  const hasExistingSession = existingSession.messages.length > 0;
  const hasThinkingEntry = sessionManager.getBranch().some((entry) => entry.type === "thinking_level_change");
  let model = options.model;
  let modelFallbackMessage;
  const sessionModel = getBranchSelection(sessionManager.getBranch(), (provider, modelId) => modelRuntime.getModel(provider, modelId));
  if (!model && hasExistingSession && sessionModel) {
    const restoredModel = modelRuntime.getModel(sessionModel.provider, sessionModel.modelId);
    if (restoredModel && modelRuntime.hasConfiguredAuth(restoredModel.provider)) {
      model = restoredModel;
    }
    if (!model) {
      modelFallbackMessage = `Could not restore model ${sessionModel.provider}/${sessionModel.modelId}`;
    }
  }
  if (!model) {
    const result = await findInitialModel({
      scopedModels: [],
      isContinuing: hasExistingSession,
      defaultProvider: settingsManager.getDefaultProvider(),
      defaultModelId: settingsManager.getDefaultModel(),
      defaultThinkingLevel: settingsManager.getDefaultThinkingLevel(),
      modelThinkingLevels: settingsManager.getAllModelThinkingLevels(),
      modelRuntime
    });
    model = result.model;
    if (!model) {
      modelFallbackMessage = formatNoModelsAvailableMessage();
    } else if (modelFallbackMessage) {
      modelFallbackMessage += `. Using ${model.provider}/${model.id}`;
    }
  }
  let thinkingLevel = options.thinkingLevel;
  if (thinkingLevel === void 0 && hasExistingSession) {
    thinkingLevel = hasThinkingEntry ? existingSession.thinkingLevel : settingsManager.getDefaultThinkingLevel() ?? DEFAULT_THINKING_LEVEL;
  }
  if (thinkingLevel === void 0 && model) {
    const perModel = settingsManager.getModelThinkingLevel(model.provider, model.id);
    if (perModel) {
      thinkingLevel = perModel;
    }
  }
  if (thinkingLevel === void 0) {
    thinkingLevel = settingsManager.getDefaultThinkingLevel() ?? DEFAULT_THINKING_LEVEL;
  }
  if (!model) {
    thinkingLevel = "off";
  } else {
    thinkingLevel = clampThinkingLevel(model, thinkingLevel);
  }
  const configuredDefaultToolNames = settingsManager.getDefaultTools();
  const allowedToolNames = options.tools ?? (options.noTools === "all" ? [] : void 0);
  const excludedToolNames = options.excludeTools;
  const excludedToolNameSet = excludedToolNames ? new Set(excludedToolNames) : void 0;
  const initialActiveToolNames = (options.tools ?? (options.noTools ? [] : configuredDefaultToolNames ?? DEFAULT_TOOL_NAMES)).filter((name) => !excludedToolNameSet?.has(name));
  const convertToLlmWithBlockImages = /* @__PURE__ */ __name((messages) => {
    const converted = convertToLlm(messages);
    if (!settingsManager.getBlockImages()) {
      return converted;
    }
    return converted.map((msg) => {
      if (msg.role === "user" || msg.role === "toolResult") {
        const content = msg.content;
        if (Array.isArray(content)) {
          const hasImages = content.some((c) => c.type === "image");
          if (hasImages) {
            const filteredContent = content.map((c) => c.type === "image" ? { type: "text", text: "Image reading is disabled." } : c).filter((c, i, arr) => (
              // Dedupe consecutive "Image reading is disabled." texts
              !(c.type === "text" && c.text === "Image reading is disabled." && i > 0 && arr[i - 1].type === "text" && arr[i - 1].text === "Image reading is disabled.")
            ));
            return { ...msg, content: filteredContent };
          }
        }
      }
      return msg;
    });
  }, "convertToLlmWithBlockImages");
  const extensionRunnerRef = {};
  const cacheWarmer = new CacheWarmer(modelRuntime, sessionManager, () => settingsManager.getCacheWarmingMode(), async (event) => extensionRunnerRef.current?.emitCacheWarmingDecision(event) ?? event.action);
  const buildRequestOptions = /* @__PURE__ */ __name((requestModel, options2 = {}) => {
    const providerRetrySettings = settingsManager.getProviderRetrySettings();
    const httpIdleTimeoutMs = settingsManager.getHttpIdleTimeoutMs();
    const effectiveTimeoutMs = httpIdleTimeoutMs === 0 ? 2147483647 : httpIdleTimeoutMs;
    const headerRunner = extensionRunnerRef.current;
    return {
      ...options2,
      timeoutMs: options2.timeoutMs ?? providerRetrySettings.timeoutMs ?? effectiveTimeoutMs,
      websocketConnectTimeoutMs: options2.websocketConnectTimeoutMs ?? settingsManager.getWebSocketConnectTimeoutMs(),
      maxRetries: options2.maxRetries ?? providerRetrySettings.maxRetries,
      maxRetryDelayMs: options2.maxRetryDelayMs ?? providerRetrySettings.maxRetryDelayMs,
      transformHeaders: /* @__PURE__ */ __name(async (requestHeaders) => {
        const headers = mergeProviderAttributionHeaders(requestModel, settingsManager, options2.sessionId, requestHeaders);
        return headerRunner?.hasHandlers("before_provider_headers") ? headerRunner.emitBeforeProviderHeaders(headers ?? {}) : headers ?? {};
      }, "transformHeaders")
    };
  }, "buildRequestOptions");
  const cacheContextIsCurrent = /* @__PURE__ */ __name((requestModel) => {
    const messages = agent.state.messages;
    return () => {
      const currentModel = agent.state.model;
      const currentMessages = agent.state.messages;
      return currentModel.provider === requestModel.provider && currentModel.id === requestModel.id && messages.length <= currentMessages.length && messages.every((message, index) => currentMessages[index] === message);
    };
  }, "cacheContextIsCurrent");
  const transformProviderPayload = /* @__PURE__ */ __name(async (payload) => {
    const runner = extensionRunnerRef.current;
    if (!runner?.hasHandlers("before_provider_request"))
      return payload;
    return runner.emitBeforeProviderRequest(payload);
  }, "transformProviderPayload");
  const handleProviderResponse = /* @__PURE__ */ __name(async (response) => {
    const runner = extensionRunnerRef.current;
    if (!runner?.hasHandlers("after_provider_response"))
      return;
    await runner.emit({
      type: "after_provider_response",
      status: response.status,
      headers: response.headers
    });
  }, "handleProviderResponse");
  const handleProviderStreamEvent = /* @__PURE__ */ __name(async (data, model2) => {
    const runner = extensionRunnerRef.current;
    if (!runner?.hasHandlers("provider_stream_event"))
      return;
    await runner.emit({
      data,
      type: "provider_stream_event",
      provider: model2.provider,
      api: model2.api,
      model: model2.id
    });
  }, "handleProviderStreamEvent");
  const agent = new Agent({
    initialState: {
      systemPrompt: "",
      model,
      thinkingLevel,
      tools: [],
      messages: existingSession.messages
    },
    convertToLlm: convertToLlmWithBlockImages,
    streamFn: /* @__PURE__ */ __name(async (model2, context, options2) => {
      const requestOptions = buildRequestOptions(model2, options2);
      if (options2?.sessionId === sessionManager.getSessionId()) {
        cacheWarmer.start({ model: model2, context, options: requestOptions }, cacheContextIsCurrent(model2));
      }
      return modelRuntime.streamSimple(model2, context, requestOptions);
    }, "streamFn"),
    onPayload: transformProviderPayload,
    onResponse: handleProviderResponse,
    onProviderStreamEvent: handleProviderStreamEvent,
    sessionId: sessionManager.getSessionId(),
    transformContext: /* @__PURE__ */ __name(async (messages) => {
      const runner = extensionRunnerRef.current;
      if (!runner)
        return messages;
      return runner.emitContext(messages);
    }, "transformContext"),
    steeringMode: settingsManager.getSteeringMode(),
    followUpMode: settingsManager.getFollowUpMode(),
    transport: settingsManager.getTransport(),
    thinkingBudgets: settingsManager.getThinkingBudgets(),
    maxRetryDelayMs: settingsManager.getProviderRetrySettings().maxRetryDelayMs
  });
  if (hasExistingSession) {
    if (!hasThinkingEntry) {
      sessionManager.appendThinkingLevelChange(thinkingLevel);
    }
  } else {
    if (model) {
      sessionManager.appendModelChange(model.provider, model.id);
    }
    sessionManager.appendThinkingLevelChange(thinkingLevel);
  }
  const session = new AgentSession({
    agent,
    sessionManager,
    settingsManager,
    cwd,
    scopedModels: options.scopedModels,
    resourceLoader,
    customTools: options.customTools,
    modelRuntime,
    cacheWarmer,
    initialActiveToolNames,
    usesDefaultTools: options.tools === void 0 && !options.noTools,
    allowedToolNames,
    excludedToolNames,
    extensionRunnerRef,
    sessionStartEvent: options.sessionStartEvent
  });
  const extensionsResult = resourceLoader.getExtensions();
  return {
    session,
    extensionsResult,
    modelFallbackMessage
  };
}
__name(createAgentSession, "createAgentSession");
export {
  AgentSessionRuntime,
  SessionImportFileNotFoundError,
  createAgentSession,
  createAgentSessionFromServices,
  createAgentSessionRuntime,
  createAgentSessionServices,
  createBashTool,
  createCodingTools,
  createEditTool,
  createFindTool,
  createGrepTool,
  createLsTool,
  createPowerShellTool,
  createReadOnlyTools,
  createReadTool,
  createWriteTool,
  withFileMutationQueue
};
