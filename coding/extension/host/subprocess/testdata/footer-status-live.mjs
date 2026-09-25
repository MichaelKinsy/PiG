// Mirrors parity/scenarios/extensions-runtime/testdata/ext/footer-status.mjs:
// a footer factory that composes its lines from
// footerData.getExtensionStatuses() instead of a closed-over value, so a
// later status change is only visible if the host pushes a fresh
// state_update and the runtime re-renders the footer from it.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setStatus("k", "one");
    ctx.ui.setFooter((_tui, _theme, footerData) => ({
      render() {
        const statuses = [...footerData.getExtensionStatuses().values()];
        return ["footer", ...statuses];
      },
      invalidate() {},
    }));
  });

  pi.registerCommand("bump-status", {
    description: "Change the keyed status without touching the footer factory",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("k", "two");
    },
  });
}
