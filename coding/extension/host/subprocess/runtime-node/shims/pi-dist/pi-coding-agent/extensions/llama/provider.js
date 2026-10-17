import { isModelType, } from "../../../pi-ai/sdk-bundle/index.js";
import { llamaCppClassifyApi } from "../../../pi-ai/api/llama-cpp-classify.lazy.js";
import { typesafeSystemOneApi } from "../../../pi-ai/api/typesafe-system-one.lazy.js";
import { stream, streamSimple } from "../../../pi-ai/sdk-bundle/compat.js";
import { LlamaClient, llamaInferenceUrl, normalizeLlamaServerUrl, } from "./client.js";
export const LLAMA_PROVIDER_ID = "llama.cpp";
export const DEFAULT_LLAMA_SERVER_URL = "http://127.0.0.1:8080";
function credentialServerUrl(credential) {
    const value = credential?.env?.LLAMA_BASE_URL;
    return typeof value === "string" && value.trim() ? normalizeLlamaServerUrl(value) : undefined;
}
async function resolveServerUrl(ctx, credential) {
    const configured = credentialServerUrl(credential) ?? (await ctx.env("LLAMA_BASE_URL"))?.trim();
    return configured ? normalizeLlamaServerUrl(configured) : undefined;
}
function modelIsSelectable(model, routerAutoload) {
    if (model.status.value === "loaded")
        return true;
    // llama.cpp reports idle-slept models as "sleeping"; requests wake them automatically.
    if (model.status.value === "sleeping")
        return true;
    // Unloaded presets are routable only when llama.cpp router autoload can load them on first use.
    return routerAutoload && model.status.value === "unloaded" && !model.status.failed && model.source === "preset";
}
async function routerAutoloadEnabled(client, catalog, signal) {
    if (!catalog.some((model) => model.status.value === "unloaded" && model.source === "preset"))
        return false;
    try {
        return (await client.props({ signal })).models_autoload === true;
    }
    catch {
        return false;
    }
}
function configuredContextWindow(model) {
    const args = model.status.args ?? [];
    for (let index = 0; index < args.length - 1; index++) {
        const flag = args[index];
        if (flag !== "--ctx-size" && flag !== "-c" && flag !== "-ctx")
            continue;
        const contextWindow = Number(args[index + 1]);
        if (Number.isSafeInteger(contextWindow) && contextWindow > 0)
            return contextWindow;
    }
    return undefined;
}
function contextWindowOf(model, cachedContextWindow) {
    const runtimeContextWindow = model.meta?.n_ctx;
    if (runtimeContextWindow && runtimeContextWindow > 0)
        return runtimeContextWindow;
    const configuredContext = configuredContextWindow(model);
    if (configuredContext)
        return configuredContext;
    if (cachedContextWindow && cachedContextWindow > 0)
        return cachedContextWindow;
    const trainingContextWindow = model.meta?.n_ctx_train;
    return trainingContextWindow && trainingContextWindow > 0 ? trainingContextWindow : 128000;
}
/**
 * Whether llama.cpp reports a native decision model. Since llama.cpp 0.6.0, `GET /models` lists
 * `decisions` in `architecture.output_modalities` for these models, including unloaded and sleeping ones.
 * Older servers report `["text"]` or omit `architecture`, so their models are treated as chat models.
 */
function isDecisionModel(model) {
    return model.architecture?.output_modalities?.includes("decisions") === true;
}
/** Decision-only models cannot generate text and are not listed for chat. */
function isChatModel(model) {
    return !isDecisionModel(model) || model.architecture?.output_modalities?.includes("text") === true;
}
/**
 * A llama.cpp model used as a classifier. Decision models answer natively through llama.cpp's
 * System One endpoint (`/v1/systemone`). Chat models fall back to `llama-cpp-classify`, which reads
 * answers from next-token label probabilities.
 */
function toPiClassifierModel(model, serverUrl, cachedContextWindow) {
    const decision = isDecisionModel(model);
    return {
        type: "classifier",
        id: model.id,
        name: model.id,
        api: decision ? "typesafe-system-one" : "llama-cpp-classify",
        provider: LLAMA_PROVIDER_ID,
        baseUrl: decision ? llamaInferenceUrl(serverUrl) : serverUrl,
        input: ["text"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow: contextWindowOf(model, cachedContextWindow),
    };
}
function isLlamaClassifierModel(model) {
    return (isModelType(model, "classifier") && (model.api === "llama-cpp-classify" || model.api === "typesafe-system-one"));
}
function toPiModel(model, serverUrl, props, cachedContextWindow) {
    const contextWindow = contextWindowOf(model, cachedContextWindow);
    const reasoning = props?.chat_template?.includes("enable_thinking") === true;
    return {
        id: model.id,
        name: model.id,
        api: "openai-completions",
        provider: LLAMA_PROVIDER_ID,
        baseUrl: llamaInferenceUrl(serverUrl),
        reasoning,
        ...(reasoning && {
            thinkingLevelMap: { off: "off", minimal: null, low: null, medium: "medium", high: null, xhigh: null },
        }),
        input: model.architecture?.input_modalities?.includes("image") ? ["text", "image"] : ["text"],
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
        contextWindow,
        maxTokens: contextWindow,
        compat: {
            supportsStore: false,
            supportsDeveloperRole: false,
            supportsReasoningEffort: false,
            supportsUsageInStreaming: true,
            supportsStrictMode: false,
            maxTokensField: "max_tokens",
            ...(reasoning && { thinkingFormat: "qwen-chat-template" }),
        },
    };
}
export function createLlamaProvider() {
    let models = [];
    let classifiers = [];
    const fallbackClassifier = llamaCppClassifyApi();
    const decisionClassifier = typesafeSystemOneApi();
    const setCatalog = (catalog, serverUrl, options = {}) => {
        const selectable = catalog.filter((model) => modelIsSelectable(model, options.routerAutoload === true));
        models = selectable.filter(isChatModel).map((model) => toPiModel(model, serverUrl));
        classifiers = selectable.map((model) => toPiClassifierModel(model, serverUrl));
    };
    const provider = {
        id: LLAMA_PROVIDER_ID,
        name: "llama.cpp",
        baseUrl: llamaInferenceUrl(DEFAULT_LLAMA_SERVER_URL),
        auth: {
            apiKey: {
                name: "llama.cpp server",
                login: async (interaction) => {
                    const enteredUrl = await interaction.prompt({
                        type: "text",
                        message: "llama.cpp server URL",
                        placeholder: process.env.LLAMA_BASE_URL ?? DEFAULT_LLAMA_SERVER_URL,
                    });
                    const serverUrl = normalizeLlamaServerUrl(enteredUrl.trim() || process.env.LLAMA_BASE_URL || DEFAULT_LLAMA_SERVER_URL);
                    const apiKey = (await interaction.prompt({
                        type: "secret",
                        message: "API key (optional)",
                    })).trim();
                    await new LlamaClient(serverUrl, apiKey || undefined).list({ signal: interaction.signal });
                    return {
                        type: "api_key",
                        key: apiKey || undefined,
                        env: { LLAMA_BASE_URL: serverUrl },
                    };
                },
                check: async ({ ctx, credential }) => {
                    const serverUrl = await resolveServerUrl(ctx, credential);
                    return serverUrl
                        ? { type: "api_key", source: credential ? "stored credential" : "LLAMA_BASE_URL" }
                        : undefined;
                },
                resolve: async ({ ctx, credential }) => {
                    const serverUrl = await resolveServerUrl(ctx, credential);
                    if (!serverUrl)
                        return undefined;
                    const apiKey = credential?.key ?? (await ctx.env("LLAMA_API_KEY")) ?? "local";
                    return {
                        auth: { apiKey, baseUrl: llamaInferenceUrl(serverUrl) },
                        env: { ...credential?.env, LLAMA_BASE_URL: serverUrl },
                        source: credential ? "stored credential" : "LLAMA_BASE_URL",
                    };
                },
            },
        },
        getModels: () => models,
        getAllModels: () => [...models, ...classifiers],
        refreshModels: async (context) => {
            const cachedContextWindows = new Map();
            if (context.stored) {
                const stored = context.stored.models.filter((model) => model.provider === LLAMA_PROVIDER_ID);
                const restored = stored.filter((model) => isModelType(model, "chat") && model.api === "openai-completions");
                const restoredClassifiers = stored.filter(isLlamaClassifierModel);
                for (const model of [...restored, ...restoredClassifiers]) {
                    cachedContextWindows.set(model.id, model.contextWindow);
                }
                if (!(await context.publish({
                    update: () => {
                        models = restored;
                        classifiers = restoredClassifiers;
                    },
                }))) {
                    return;
                }
            }
            if (!context.allowNetwork || context.signal.aborted || context.credential?.type !== "api_key")
                return;
            const serverUrl = credentialServerUrl(context.credential);
            if (!serverUrl)
                return;
            const client = new LlamaClient(serverUrl, context.credential.key);
            const catalog = await client.list({ signal: context.signal });
            if (context.signal.aborted)
                return;
            const routerAutoload = await routerAutoloadEnabled(client, catalog, context.signal);
            if (context.signal.aborted)
                return;
            const selectable = catalog.filter((model) => modelIsSelectable(model, routerAutoload));
            // Only loaded models expose their chat template without side effects. Unloaded autoload presets would
            // need to be loaded, while querying sleeping models may wake them. Those models remain without thinking
            // support until they are loaded and a later catalog refresh discovers it. Decision models need no
            // template, and llama.cpp reports them in the catalog regardless of their status.
            const refreshed = await Promise.all(selectable.filter(isChatModel).map(async (model) => {
                const cachedContextWindow = cachedContextWindows.get(model.id);
                if (model.status.value !== "loaded")
                    return toPiModel(model, serverUrl, undefined, cachedContextWindow);
                const props = await client.props({ model: model.id, signal: context.signal });
                return toPiModel(model, serverUrl, props, cachedContextWindow);
            }));
            const refreshedClassifiers = selectable.map((model) => toPiClassifierModel(model, serverUrl, cachedContextWindows.get(model.id)));
            if (context.signal.aborted)
                return;
            await context.publish({
                persist: { models: [...refreshed, ...refreshedClassifiers], checkedAt: Date.now() },
                update: () => {
                    models = refreshed;
                    classifiers = refreshedClassifiers;
                },
            });
        },
        stream: (model, context, options) => stream(model, context, options),
        streamSimple: (model, context, options) => streamSimple(model, context, options),
        classify: (model, context, options) => (model.api === "typesafe-system-one" ? decisionClassifier : fallbackClassifier).classify(model, context, options),
    };
    return { provider, setCatalog };
}
//# sourceMappingURL=provider.js.map