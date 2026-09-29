// A centered overlay tall enough to cover the editor row, shaped like
// pi-rtk-optimizer's /rtk settings panel.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setStatus("cover-probe-ready", "COVER-PROBE-READY");
  });
  pi.registerCommand("cover", {
    description: "Open an overlay over the editor",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("cover-probe-ready", undefined);
      await ctx.ui.custom(
        (_tui, theme, _kb, done) => ({
          render(width) {
            return Array.from({ length: 31 }, (_, i) => {
              const text = ` COVER ${String(i + 1).padStart(2, "0")}`.padEnd(width - 2).slice(0, width - 2);
              return theme.fg("accent", "│") + text + theme.fg("accent", "│");
            });
          },
          handleInput(data) {
            if (data === "\x1b") done(undefined);
          },
          invalidate() {},
        }),
        { overlay: true, overlayOptions: { anchor: "center", width: 20 } },
      );
    },
  });
}
