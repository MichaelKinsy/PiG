// Registers a configuration Provider whose streamSimple is a method, so its receiver is observable. Pi calls extension.streamSimple with the effective registered root (provider-composer.ts:500-501).
let receiver;
export default function (pi) {
  const config = {
    name: "Root",
    api: "config-root-api",
    baseUrl: "https://root.invalid",
    apiKey: "root-key",
    models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }],
    streamSimple(model) {
      receiver = this;
      const message = { role: "assistant", api: model.api, provider: model.provider, model: model.id, content: [{ type: "text", text: "root" }], usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 1 };
      return { async *[Symbol.asyncIterator]() { yield { type: "done", reason: "stop", message }; }, result: async () => message };
    },
  };
  pi.registerProvider("config-root", config);
  pi.registerCommand("root-check", {
    handler: async (_args, ctx) => {
      const model = ctx.modelRegistry.find("config-root", "m");
      if (!model) throw new Error("config-root model is not registered");
      await ctx.modelRegistry.streamSimple(model, { messages: [{ role: "user", content: "hi", timestamp: 1 }] }).result();
      const root = ctx.modelRegistry.getRegisteredProviderConfig("config-root");
      if (receiver !== root) throw new Error(`streamSimple receiver is ${receiver === undefined ? "undefined" : receiver === config ? "the author's object" : "not the effective root"}`);
      pi.events.emit("config-root", root);
    },
  });
}
