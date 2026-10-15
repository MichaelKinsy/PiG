// model_registry_refresh checks ctx.modelRegistry against the objects Pi's registry holds after the same steps
// (testdata/model-registry-oracle.mjs): args.objects maps each model id to JSON.stringify of Pi's model object, or null for a
// model Pi does not have; args.auth, when set, is the headers getApiKeyAndHeaders resolves for every present model.
export default function (pi) {
  pi.registerCommand("model_registry_refresh", {
    description: "verify model registry refresh",
    handler: async (args, ctx) => {
      const expected = JSON.parse(args);
      const provider = expected.provider || "registry-refresh";
      for (const [modelId, want] of Object.entries(expected.objects)) {
        const model = ctx.modelRegistry.find(provider, modelId);
        const got = model === undefined ? null : JSON.stringify(model);
        if (got !== want) throw new Error(`${provider}/${modelId}:\n got ${got}\nwant ${want}`);
        if (model && expected.auth) {
          const auth = await ctx.modelRegistry.getApiKeyAndHeaders(model);
          if (JSON.stringify(auth.headers) !== JSON.stringify(expected.auth.headers)) throw new Error(`${modelId}.auth.headers = ${JSON.stringify(auth.headers)}, want ${JSON.stringify(expected.auth.headers)}`);
        }
      }
    },
  });
}
