export default function (pi) {
  let startupComplete = false;
  let startupDraft = "";
  pi.on("session_start", async (_event, ctx) => {
    await new Promise((resolve) => {
      const dispose = ctx.ui.onTerminalInput((data) => {
        if (data !== "~") return undefined;
        startupDraft = ctx.ui.getEditorText();
        dispose();
        resolve();
        return { consume: true };
      });
      ctx.ui.notify("STARTUP INPUT HOLD", "info");
    });
    startupComplete = true;
  });
  pi.on("input", (event, ctx) => {
    ctx.ui.notify(`STARTUP INPUT ${startupComplete ? "READY" : "EARLY"}: ${event.text}; draft=${startupDraft}`, "info");
    return { action: "handled" };
  });
}
