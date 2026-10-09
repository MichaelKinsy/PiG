// Pi 0.87.1 model-runtime.ts:744-750 retains the supplied Provider object.
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

export default function (pi) {
  let models = [{
    id: "carrier-model", name: "Carrier", provider: "carrier-provider",
    api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false,
    input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 4000, maxTokens: 100,
  }];
  const stream = (model, context, options = {}) => {
    const events = createAssistantMessageEventStream();
    const message = {
      role: "assistant", api: model.api, provider: model.provider, model: model.id,
      content: [], usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
        cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
      stopReason: "stop", timestamp: 1,
    };
    if (options.metadata?.fail) throw new Error("carrier stream failed");
    if (options.metadata?.wait) {
      const abort = () => {
        message.stopReason = "aborted";
        message.errorMessage = "carrier cancelled";
        events.push({ type: "error", reason: "aborted", error: message });
      };
      if (options.signal.aborted) abort();
      else options.signal.addEventListener("abort", abort, { once: true });
    } else {
      message.content = [{ type: "text", text: options.metadata?.method ?? "carrier answer" }];
      events.push({ type: "done", reason: "stop", message });
    }
    return events;
  };
  const provider = {
    id: "carrier-provider", name: "Carrier Provider", baseUrl: models[0].baseUrl,
    headers: { "X-Carrier": "present" },
    auth: {
      apiKey: {
        name: "Carrier API key",
        check: async ({ ctx }) => ({ type: "api_key", source: await ctx.env("CARRIER_SOURCE") }),
        resolve: async ({ ctx, credential }) => ({ auth: { apiKey: credential?.key ?? await ctx.env("CARRIER_KEY") }, source: "caller context" }),
        login: async interaction => ({ type: "api_key", key: await interaction.prompt({ type: "secret", message: "Carrier key" }) }),
      },
      oauth: {
        name: "Carrier OAuth", isSubscription: true, loginLabel: "Carrier login",
        login: async interaction => ({ type: "oauth", access: await interaction.prompt({ type: "text", message: "Carrier OAuth" }), refresh: "refresh", expires: 100 }),
        refresh: async credential => ({ ...credential, access: "rotated", expires: 200 }),
        toAuth: async credential => ({ apiKey: credential.access, baseUrl: "https://carrier.invalid" }),
      },
    },
    getModels: () => models,
    getAllModels: () => [...models, { ...models[0], id: "carrier-all-only" }],
    filterModels: (input, credential) => credential?.key === "selected" ? input.slice(0, 1) : [],
    filterAllModels: (input, credential) => credential?.key === "all" ? input : [],
    refreshModels: async context => {
      if (!context.force) return;
      await context.publish({ persist: null, update: () => { models = [...models, { ...models[0], id: "refreshed" }]; } });
    },
    stream,
    streamSimple: (model, context, options) => stream(model, context, { ...options, metadata: { method: "simple" } }),
    fetchDeferred: (model, handle, options) => stream(model, { messages: [] }, { ...options, metadata: { method: handle.id } }),
    cancelDeferred: async (_model, handle) => { if (handle.id !== "deferred") throw new Error("wrong deferred handle"); },
  };
  pi.registerProvider(provider);
  pi.registerCommand("carrier-remove", { handler: () => pi.unregisterProvider(provider.id) });
  pi.registerCommand("carrier-crash", { handler: () => process.exit(43) });
  pi.events.on("carrier:identity", reply => reply(provider));
}
