import type {
  ExecuteToolOptions,
  Extension,
  ExtensionAPI,
  ExtensionToolContext,
  ExtensionVirtualModel,
  InlineExtension,
  McpServerConfig,
  McpServersChangeEvent,
  ModelRegistry,
  ProviderConfig,
  ProviderStreamEvent,
  RegisteredMcpServer,
  ToolAnnotations,
  ToolDefinition,
  ToolExposure,
  ToolLoadout,
  ToolLoadoutChanges,
  ToolNamespace,
} from "@earendil-works/pi-coding-agent";
import type {
  ExtensionToolContext as AdapterExtensionToolContext,
  McpServerConfig as AdapterMcpServerConfig,
  ModelRegistry as AdapterModelRegistry,
  ProviderConfig as AdapterProviderConfig,
  PiGLoginDefinition,
  PiGSpriteDefinition,
} from "@michaelkinsy/pig-extension-types";

const login: PiGLoginDefinition = {
  brand: ["."],
  hero: ["."],
  mascot: ["."],
  palette: {},
  name: "Example",
  description: "Type declaration check",
  tagline: "Pi-compatible extension",
};

const sprite: PiGSpriteDefinition = {
  id: "example",
  name: "Example",
  tagline: "Type declaration check",
  mascot: ["."],
  palette: {},
};

export default function extension(pi: ExtensionAPI): void {
  pi.on("session_start", async (_event, ctx) => {
    await ctx.ui.setLogin(login);
    await ctx.ui.registerSprite(sprite);
    ctx.ui.notify("ready", "info");
  });
  // PiG emits Pi's UI prompt lifecycle for every SDK; the kinds and the
  // optional title come from the pinned Pi declarations.
  pi.on("ui_prompt_start", (event) => {
    const kind: "select" | "confirm" | "input" | "editor" | "custom" = event.kind;
    const title: string | undefined = event.title;
    void [kind, title, event.reason satisfies "ui_prompt"];
  });
  pi.on("context_with_system", (event) => ({ messages: event.messages }));
  pi.on("agent_before_settle", (event) => {
    const outcome: "completed" | "aborted" | "error" = event.outcome;
    const canContinue: boolean = event.context.canContinue;
    void [outcome, canContinue, event.context.contextEntries, event.context.llmMessages];
    return { entries: event.entries, continue: event.continue };
  });
  pi.on("turn_end", (event) => {
    const messageEntryId: string = event.messageEntryId;
    const toolResultEntryIds: string[] = event.toolResultEntryIds;
    void [messageEntryId, toolResultEntryIds];
  });
  pi.on("ui_prompt_end", (event) => {
    void [event.kind, event.title];
  });
}

// The 0.99.1 extension API additions, exactly as the pinned declarations
// spell them (.upstream/v0.99.1/packages/coding-agent/src/core/extensions/types.ts).
// Each row fails to compile against the 0.87.1 declarations.
const exposures = ["direct", "model-only", "codemode", "deferred", "hidden"] as const satisfies readonly ToolExposure[];
const annotations = {
  readOnlyHint: true,
  destructiveHint: false,
  idempotentHint: true,
  openWorldHint: false,
} satisfies ToolAnnotations;
const namespace = { name: "mcp__docs", description: "Docs server" } satisfies ToolNamespace;
const parameters = {} as ToolDefinition["parameters"];

export function toolDefinitionFields(): ToolDefinition {
  return {
    name: "lookup",
    label: "Lookup",
    description: "Look a name up",
    parameters,
    outputSchema: {} as ToolDefinition["parameters"],
    exposure: exposures[3],
    namespace,
    annotations,
    defaultActive: false,
    prepareLoadout: (loadout: ToolLoadout): ToolLoadoutChanges | undefined => {
      const exposure: ToolExposure = loadout.getExposure("lookup");
      const group: ToolNamespace | undefined = loadout.getNamespace("lookup");
      void [exposure, group, loadout.declared.length, loadout.callable.length, loadout.registered.length];
      return { descriptions: { lookup: "Look a name up" }, hiddenDeclarations: ["lookup"] };
    },
    async execute(_toolCallId, _params, _signal, _onUpdate, ctx: ExtensionToolContext) {
      const options: ExecuteToolOptions = { signal: new AbortController().signal, onUpdate: () => {} };
      const outcome = await ctx.executeTool("read", { path: "a" }, options);
      void [outcome, ctx.tools.length];
      return { content: [], details: undefined, structuredContent: { ok: true } };
    },
  };
}

export function apiAdditions(pi: ExtensionAPI): void {
  const config: McpServerConfig = { url: "https://mcp.example.com/jira" };
  pi.registerMcpServer("jira", config);
  const servers: RegisteredMcpServer[] = pi.getMcpServers();
  void servers;
  pi.unregisterMcpServer("jira");

  const model: ExtensionVirtualModel<{ turn: number }> = {
    provider: "router",
    id: "auto",
    name: "Auto",
    thinkingLevels: ["off", "high"],
    route: (request, ctx) => {
      void [request.reason, request.state?.turn, request.thinkingLevel, ctx.cwd];
      return { model: request.model, thinkingLevel: "off", state: { turn: 1 } };
    },
  };
  pi.registerVirtualModel(model);
  pi.unregisterVirtualModel("router", "auto");

  const settings: object = pi.getSettings();
  void settings;

  pi.on("mcp_servers_change", (event: McpServersChangeEvent) => {
    const changed: RegisteredMcpServer[] = event.servers;
    void [changed, event.type satisfies "mcp_servers_change"];
  });
  pi.on("provider_stream_event", (event: ProviderStreamEvent) => {
    void [event.provider, event.api, event.model, event.data, event.type satisfies "provider_stream_event"];
  });
  pi.on("tool_call", (event) => {
    const parent: string | undefined = event.parentToolCallId;
    void parent;
  });
  pi.on("tool_result", (event) => {
    const parent: string | undefined = event.parentToolCallId;
    const structured = event.structuredContent;
    void [parent, structured, event.isError];
    return { structuredContent: { redacted: true }, isError: false };
  });
  pi.on("tool_execution_start", (event) => {
    const parent: string | undefined = event.parentToolCallId;
    void parent;
  });
  pi.on("tool_execution_update", (event) => {
    const parent: string | undefined = event.parentToolCallId;
    void parent;
  });
  pi.on("tool_execution_end", (event) => {
    const parent: string | undefined = event.parentToolCallId;
    void [parent, event.isError];
  });

  const provider: ProviderConfig = {
    models: [
      { id: "chat", name: "Chat", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, reasoning: false, contextWindow: 1, maxTokens: 1 },
      { type: "image", id: "img", name: "Image", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, output: ["image"] },
      { type: "classifier", id: "cls", name: "Classifier", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1 },
    ],
  };
  pi.registerProvider("lab", provider);
}

// Typed model registry and provider image/classifier operations (model-registry.ts:135-177; types.ts:1385-1387).
// The declarations are the pinned package's, so a row is a use that must compile, or a misuse that must not.
type Classify = ModelRegistry["classify"];
type ClassifierModelArg = Parameters<Classify>[0];
type ClassifierContextArg = Parameters<Classify>[1];
type ClassifierOptionsArg = Parameters<Classify>[2];

export async function typedModelRegistry(registry: ModelRegistry, classifier: ClassifierModelArg, request: ClassifierContextArg, options: ClassifierOptionsArg) {
  const classifiers = registry.getModelsOfType("classifier");
  const images = registry.getModelsOfType("image", "lab");
  const chat = registry.getModelsOfType("chat");
  const found = registry.findOfType("classifier", "lab", "cls");
  const same = registry.getModelOfType("image", "lab", "img");
  const available = await registry.getAvailableOfType("classifier", "lab", { signal: new AbortController().signal });
  const result = await registry.classify(classifier, request, options);
  void [classifiers[0]?.contextWindow, images[0]?.output, chat[0]?.api, found?.type satisfies "classifier" | undefined, same?.type satisfies "image" | undefined, available, result.stopReason, result.answers, result.usage];
  registry.registerVirtualModel({ provider: "router", id: "auto", name: "Auto", route: (input) => ({ model: input.model, thinkingLevel: "off" }) });
  registry.unregisterVirtualModel("router", "auto");
  // @ts-expect-error a model type outside chat, image and classifier is rejected
  registry.getModelsOfType("video");
  // @ts-expect-error classify takes a classifier model, not a chat model
  await registry.classify(chat[0], request);
  // @ts-expect-error the type argument selects the model type: an image model is not a classifier model
  const notClassifier: "classifier" | undefined = registry.findOfType("image", "lab", "img")?.type;
  void notClassifier;
}

// model-registry.ts:181-188 (1.0.0): generateImages takes an image model and an images context, and never rejects.
export async function generateImagesFromRegistry(registry: ModelRegistry) {
  const painter = registry.getModelOfType("image", "openrouter", "google/gemini-2.5-flash-image");
  if (!painter) return;
  const result = await registry.generateImages(painter, { input: [{ type: "text", text: "a red circle" }] }, { signal: new AbortController().signal });
  void [result.stopReason, result.output, result.errorMessage, result.usage];
  const classifier = registry.getModelOfType("classifier", "lab", "cls");
  // @ts-expect-error generateImages takes an image model, not a classifier model
  if (classifier) await registry.generateImages(classifier, { input: [] });
}

export function extensionRegistryFromContext(pi: ExtensionAPI): void {
  pi.registerCommand("typed", {
    handler: async (_args, ctx) => {
      const registry: ModelRegistry = ctx.modelRegistry;
      const [first] = await registry.getAvailableOfType("classifier");
      if (first) {
        const result = await registry.classify(first, { state: { text: "hi" }, questions: {} });
        void result.errorMessage;
      }
    },
  });
}

export const providerImagesAndClassifiers: ProviderConfig = {
  baseUrl: "https://lab.test",
  apiKey: "key",
  models: [
    { type: "image", id: "img", name: "Image", api: "lab-images", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, output: ["image"] },
    { type: "classifier", id: "cls", name: "Classifier", api: "lab-classifier", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1 },
  ],
  images: {
    "lab-images": {
      generateImages: async (model, context, options) => {
        void [model.output, context.input, options?.signal];
        return { api: model.api, provider: model.provider, model: model.id, output: [], stopReason: "stop", timestamp: 0 };
      },
    },
  },
  classifiers: {
    "lab-classifier": {
      classify: async (model, context, options) => {
        void [context.state, context.questions, options?.apiKey, options?.headers];
        return { api: model.api, provider: model.provider, model: model.id, answers: {}, stopReason: "stop", timestamp: 0 };
      },
    },
  },
};

export function inlineExtensionFlags(loaded: Extension): InlineExtension {
  const replaceable: boolean | undefined = loaded.replaceable;
  return { name: "mcp", factory: () => {}, hidden: true, replaceable: replaceable ?? true, builtin: true };
}

// The adapter re-exports the pinned declarations: its names are the same types.
type Same<A, B> = (<T>() => T extends A ? 1 : 2) extends (<T>() => T extends B ? 1 : 2) ? true : false;
export const adapterReexportsPinnedTypes: [
  Same<AdapterExtensionToolContext, ExtensionToolContext>,
  Same<AdapterMcpServerConfig, McpServerConfig>,
  Same<AdapterModelRegistry, ModelRegistry>,
  Same<AdapterProviderConfig, ProviderConfig>,
] = [true, true, true, true];
