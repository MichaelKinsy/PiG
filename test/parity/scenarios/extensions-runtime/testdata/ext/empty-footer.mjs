// Shaped like pi-powerline-footer's startup: the footer is replaced with a
// component that renders no lines, and a widget sits above the editor.
export default function (pi) {
  pi.registerCommand("footer-probe", {
    handler: (_args, ctx) => ctx.ui.notify("EMPTY FOOTER HEADER", "info"),
  });
  pi.on("session_start", async (_event, ctx) => {
    if (!ctx.hasUI) return;
    ctx.ui.setWidget("empty-footer-probe", ["EMPTY FOOTER PROBE"]);
    ctx.ui.setFooter(() => ({ render: () => [], invalidate() {} }));
  });
}
