import assert from "node:assert/strict";
import triggerCompactExtension from "./trigger-compact.ts";

// upstream: packages/coding-agent/test/trigger-compact-extension.test.ts:28-60.
// The spy belongs at ctx.compact in the extension, not after the host's asynchronous compaction dispatch.
export default function (pi) {
  let calls = 0;
  triggerCompactExtension({
    on(event, handler) {
      if (event !== "turn_end") return;
      assert.equal(typeof handler, "function");
      pi.on(event, (event, ctx) => {
        const context = Object.create(ctx);
        context.compact = () => { calls++; };
        handler(event, context);
        return calls;
      });
    },
    registerCommand() {},
  });
}
