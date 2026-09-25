export default function (pi) {
  pi.registerProvider("openai", { baseUrl: "https://omitted.invalid/v1" });
  pi.registerProvider("anthropic", { baseUrl: "https://empty.invalid/v1", models: [] });
  pi.registerProvider("node-collision", {
    baseUrl: "https://collision.invalid/v1",
    api: "openai-responses",
    apiKey: "node-key",
    authHeader: false,
    headers: { "X-Provider-Dupe": "first", "x-provider-dupe": "second" },
    models: [{
      id: "model",
      name: "Model",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 1000,
      maxTokens: 100,
      headers: { "X-Model-Dupe": "first", "x-model-dupe": "second" },
    }],
  });

  pi.registerCommand("model_registry_identity", {
    description: "verify model registry identity and missing lookup behavior",
    handler: async (_args, ctx) => {
      const current = ctx.modelRegistry.find("openrouter", "org/model/name");
      if (!current) throw new Error("current model not found");
      if (current.provider !== "openrouter") {
        throw new Error(`provider = ${JSON.stringify(current.provider)}, want openrouter`);
      }
      if (current.id !== "org/model/name") {
        throw new Error(`id = ${JSON.stringify(current.id)}, want org/model/name`);
      }
      if (ctx.modelRegistry.find("missing", "org/model/name") !== undefined) {
        throw new Error("unknown model was fabricated");
      }
    },
  });
}
