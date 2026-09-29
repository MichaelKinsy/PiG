import assert from "node:assert/strict";
import customCompactionExtension from "./custom-compaction.ts";

// packages/coding-agent/test/compaction-extensions-example.test.ts:57-134.
// Only find is replaced, as in the upstream mock; complete still traverses the real Node-to-host ModelRegistry bridge.
export default function (pi) {
  customCompactionExtension({
    on(event, handler) {
      if (event !== "session_before_compact") return;
      assert.equal(typeof handler, "function");
      pi.on(event, (event, ctx) => {
        const model = {
          provider: "example-custom", api: "example-custom-api", id: "summary-model", name: "Summary Model",
          input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
          contextWindow: 1000, maxTokens: 100,
        };
        return handler(event, {
          ui: { notify() {} },
          modelRegistry: {
            find: () => model,
            complete: (selected, context, options) => {
              assert.deepEqual(selected, model);
              assert.equal(Array.isArray(context.messages), true);
              assert.equal(options.maxTokens, 8192);
              assert.equal(Object.hasOwn(options, "apiKey"), false);
              return ctx.modelRegistry.complete(selected, context, options);
            },
          },
        });
      });
    },
  });
}
