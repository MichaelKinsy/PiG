// The author of a configuration Provider. A reader in another process uses its effective root through xref (model-runtime.ts:438-443,753-766).
let config;
export default function (pi) {
  config = {
    name: "Author",
    api: "xref-config-api",
    baseUrl: "https://author.invalid",
    apiKey: "author-key",
    models: [{ id: "m1", name: "M1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 }],
    streamSimple() { throw new Error("unused"); },
  };
  pi.registerProvider("xref-config", config);
  pi.registerCommand("author-after-reader-write", {
    handler: async (_args, ctx) => {
      const root = ctx.modelRegistry.getRegisteredProviderConfig("xref-config");
      if (root === config) throw new Error("the effective root is the author's object");
      if (root.headers?.reader !== "1") throw new Error(`the reader's root write is not visible to the author: ${JSON.stringify(root.headers)}`);
      config.models.push({ ...config.models[0], id: "m2", name: "M2" });
      config.name = "Author root write";
    },
  });
  pi.registerCommand("author-after-reader-merge", {
    handler: async (_args, ctx) => {
      const root = ctx.modelRegistry.getRegisteredProviderConfig("xref-config");
      if (root.name !== "Reader") throw new Error(`merged name ${root.name}`);
      if (root.models !== config.models) throw new Error("the merged root does not hold the author's models array");
      if (root.streamSimple !== config.streamSimple) throw new Error("the merged root does not hold the author's function");
      if (root.apiKey !== "author-key") throw new Error("the merged root lost the author's apiKey");
    },
  });
}
