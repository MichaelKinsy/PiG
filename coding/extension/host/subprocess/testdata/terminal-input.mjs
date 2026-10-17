// Exercises the node shim's onTerminalInput: a pi-shaped extension subscribing
// to raw terminal input, consuming a sentinel chunk and letting others pass,
// and a handler that throws.
export default function (pi) {
  let unsubscribe = () => {};

  pi.registerCommand("term_subscribe", {
    description: "Subscribe to raw terminal input",
    handler: async (_args, ctx) => {
      unsubscribe = ctx.ui.onTerminalInput((data) => ({ consume: data === "\x1b[99~" }));
    },
  });

  pi.registerCommand("term_throw", {
    description: "Replace the subscription with a handler that throws",
    handler: async (_args, ctx) => {
      unsubscribe();
      unsubscribe = ctx.ui.onTerminalInput(() => {
        throw new Error("terminal input handler exploded");
      });
    },
  });

  pi.registerCommand("term_unsubscribe", {
    description: "Release the raw terminal input subscription",
    handler: async () => {
      unsubscribe();
    },
  });
}
