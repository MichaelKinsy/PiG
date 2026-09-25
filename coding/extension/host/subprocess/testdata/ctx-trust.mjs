// Reports ctx.isProjectTrusted() back through a notification. Extensions call
// it before touching project-scoped configuration; upstream declares it on
// ExtensionContext (types.ts:332).
export default function (pi) {
  pi.registerCommand("report_trust", {
    description: "Notify the host with the observed project trust state",
    handler: async (_args, ctx) => {
      ctx.ui.notify(`trusted=${String(ctx.isProjectTrusted())}`, "info");
    },
  });
}
