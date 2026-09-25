export default function (pi) {
  pi.registerCommand("model_registry_refresh", {
    description: "verify model registry refresh",
    handler: async (args, ctx) => {
      const expected = JSON.parse(args);
      const provider = expected.provider || "registry-refresh";
      for (const [modelId, name] of Object.entries(expected.present)) {
        const model = ctx.modelRegistry.find(provider, modelId);
        if (!model || model.name !== name) {
          throw new Error(`${modelId} = ${JSON.stringify(model)}, want name ${name}`);
        }
        for (const field of ["id", "name", "api", "provider", "baseUrl", "reasoning", "thinkingLevelMap", "input", "cost", "promptCache", "contextWindow", "maxTokens", "samplingParams", "headers", "compat"]) {
          if (!(field in model)) throw new Error(`${modelId} missing ${field}: ${JSON.stringify(model)}`);
        }
        if (model.id !== modelId) throw new Error(`${modelId} serialized id = ${model.id}`);
        for (const [field, value] of Object.entries(expected.fields || {})) {
          if (JSON.stringify(model[field]) !== JSON.stringify(value)) throw new Error(`${modelId}.${field} = ${JSON.stringify(model[field])}, want ${JSON.stringify(value)}`);
        }
        for (const [field, value] of Object.entries(expected.compat || {})) {
          if (JSON.stringify(model.compat?.[field]) !== JSON.stringify(value)) throw new Error(`${modelId}.compat.${field} = ${JSON.stringify(model.compat?.[field])}, want ${JSON.stringify(value)}`);
        }
        if (expected.auth) {
          const auth = await ctx.modelRegistry.getApiKeyAndHeaders(model);
          if (JSON.stringify(auth.headers) !== JSON.stringify(expected.auth.headers)) throw new Error(`${modelId}.auth.headers = ${JSON.stringify(auth.headers)}, want ${JSON.stringify(expected.auth.headers)}`);
        }
      }
      for (const modelId of expected.absent) {
        if (ctx.modelRegistry.find(provider, modelId) !== undefined) {
          throw new Error(`${modelId} should be absent`);
        }
      }
    },
  });
}
