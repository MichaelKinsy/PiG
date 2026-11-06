// Holds session_start until "~" arrives, records the draft at that moment, and reports it with the first prompt submitted afterwards.
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
      ctx.ui.notify("STARTUP KEYS HOLD", "info");
    });
    startupComplete = true;
  });
  pi.on("input", (event, ctx) => {
    ctx.ui.notify(`STARTUP KEYS ${startupComplete ? "READY" : "EARLY"}: ${event.text}; draft=[${startupDraft}]`, "info");
    return { action: "handled" };
  });
}
