// A reader in another process. Pi returns the same effective root object; children and functions are the author's; root writes are shared (model-runtime.ts:438-443).
let first;
export default function (pi) {
  pi.registerCommand("reader-first", {
    handler: async (_args, ctx) => {
      first = ctx.modelRegistry.getRegisteredProviderConfig("xref-config");
      if (!first) throw new Error("no registered configuration");
      if (ctx.modelRegistry.getRegisteredProviderConfig("xref-config") !== first) throw new Error("two reads returned different roots");
      if (typeof first.streamSimple !== "function") throw new Error(`streamSimple is ${typeof first.streamSimple}`);
      if (!Array.isArray(first.models) || first.models.length !== 1) throw new Error("models are not the author's array");
      first.headers = { reader: "1" };
    },
  });
  pi.registerCommand("reader-after-author-write", {
    handler: async (_args, ctx) => {
      const root = ctx.modelRegistry.getRegisteredProviderConfig("xref-config");
      if (root !== first) throw new Error("the root changed without a registration");
      if (first.models.length !== 2) throw new Error(`the author's child write is not visible: ${first.models.length}`);
      if (first.name !== "Author") throw new Error(`an author root write reached the effective root: ${first.name}`);
      pi.registerProvider("xref-config", { name: "Reader" });
      const merged = ctx.modelRegistry.getRegisteredProviderConfig("xref-config");
      if (merged === first || merged.name !== "Reader" || merged.models !== first.models || merged.apiKey !== "author-key") throw new Error("the partial registration did not merge over the author's root");
      if (first.name !== "Author") throw new Error("the previous root changed");
    },
  });
}
