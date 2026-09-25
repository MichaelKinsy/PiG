// Reports the terminal geometry visible to a ui.custom component. Upstream
// components size themselves from tui.terminal.columns/rows (tui.ts:333,
// terminal.ts:79-80); pi-atelier's sidebar and menu both read it.
export default function (pi) {
  pi.registerCommand("report_geometry", {
    description: "Open an overlay and report tui.terminal dimensions",
    handler: async (_args, ctx) => {
      await ctx.ui.custom((tui, _theme, _kb, done) => {
        ctx.ui.notify(`cols=${tui.terminal.columns} rows=${tui.terminal.rows}`, "info");
        done();
        return { render: () => ["done"] };
      });
    },
  });
}
