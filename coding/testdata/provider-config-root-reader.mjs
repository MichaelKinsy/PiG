// A second member of the author's Node cell shares Pi's single effective root and merges a partial re-registration over it (model-runtime.ts:438-443,753-766).
let emitted;
export default function (pi) {
  pi.events.on("config-root", (root) => { emitted = root; });
  pi.registerCommand("root-reader-check", {
    handler: async (_args, ctx) => {
      const root = ctx.modelRegistry.getRegisteredProviderConfig("config-root");
      if (!emitted || root !== emitted) throw new Error("the reader does not share the author's registered configuration root");
      pi.registerProvider("config-root", { name: "Reader" });
      const merged = ctx.modelRegistry.getRegisteredProviderConfig("config-root");
      if (merged === root || merged.name !== "Reader" || merged.apiKey !== "root-key" || merged.models !== root.models || merged.streamSimple !== root.streamSimple) {
        throw new Error("the re-registration did not merge over the author's root");
      }
    },
  });
}
