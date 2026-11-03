// "hold" waits for "~" and then reports completion; "after" reports whether "hold" has completed when it starts.
export default function (pi) {
  let holdDone = false;
  pi.registerCommand("hold", {
    description: "Waits for a barrier key",
    handler: async (_args, ctx) => {
      await new Promise((resolve) => {
        const dispose = ctx.ui.onTerminalInput((data) => {
          if (data !== "~") return undefined;
          dispose();
          resolve();
          return { consume: true };
        });
        ctx.ui.notify("COMMAND HOLD", "warning");
      });
      holdDone = true;
      ctx.ui.notify("COMMAND HOLD DONE", "warning");
    },
  });
  pi.registerCommand("after", {
    description: "Reports whether hold completed",
    handler: async (_args, ctx) => {
      ctx.ui.notify(`COMMAND AFTER: hold ${holdDone ? "done" : "pending"}`, "warning");
    },
  });
}
