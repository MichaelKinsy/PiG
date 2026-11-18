#!/usr/bin/env node
// Pi 18e99051be24 (#12233): the emitter of a pi.events channel whose listeners live in another extension. Pi runs every listener inside emit(), so a listener's failure
// is reported (runner.ts emitError -> rpc-mode.ts extension_error) before the command that emitted answers. The second command emits to a listener that fails after an await.
export default function (pi) {
  pi.registerCommand("events-order-boom", {
    description: "Emit demo:order, whose listeners of another extension fail",
    handler: async () => {
      pi.events.emit("demo:order", { n: 1 });
    },
  });
  pi.registerCommand("events-order-boom-async", {
    description: "Emit demo:order-async, whose listener of another extension rejects",
    handler: async () => {
      pi.events.emit("demo:order-async", { n: 1 });
    },
  });
}
