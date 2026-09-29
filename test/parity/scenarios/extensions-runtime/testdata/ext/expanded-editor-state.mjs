export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setStatus("paste-probe", "PASTE_FIXTURE_READY");
  });
  pi.registerCommand("seed-paste", {
    description: "Prepare the terminal callback paste-state probe",
    handler: (_args, ctx) => {
      const remove = ctx.ui.onTerminalInput((data) => {
        if (data !== "~") return;
        ctx.ui.notify(`PASTE_VALUE ${JSON.stringify(ctx.ui.getEditorText())} PASTE_END`, "info");
        remove();
        return { consume: true };
      });
      ctx.ui.notify("PASTE_LISTENER_READY", "info");
    },
  });
}
