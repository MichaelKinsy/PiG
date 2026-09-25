// Reports ctx.mode back through a notification. Extensions gate behaviour on
// the run mode -- pi-atelier returns early from session_start unless it is
// "tui" -- so an undefined value disables them silently.
export default function (pi) {
  pi.registerCommand("report_mode", {
    description: "Notify the host with the observed ctx.mode",
    handler: async (_args, ctx) => {
      ctx.ui.notify(`mode=${String(ctx.mode)}`, "info");
    },
  });
}
