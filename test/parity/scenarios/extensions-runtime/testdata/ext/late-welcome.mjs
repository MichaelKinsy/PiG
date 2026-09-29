// Shaped like pi-powerline-footer's startup welcome: a timer the
// session_start handler starts opens an overlay after the handler has
// returned.
export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    if (!ctx.hasUI) return;
    setTimeout(() => {
      void ctx.ui.custom(
        (_tui, theme, _kb, done) => ({
          render() {
            return [
              theme.fg("accent", "╭──────────────────╮"),
              theme.fg("accent", "│") + "   LATE WELCOME   " + theme.fg("accent", "│"),
              theme.fg("accent", "│") + " press any key... " + theme.fg("accent", "│"),
              theme.fg("accent", "╰──────────────────╯"),
            ];
          },
          handleInput() {
            done(undefined);
          },
          invalidate() {},
        }),
        { overlay: true, overlayOptions: { anchor: "center", width: 20 } },
      );
    }, 100);
  });
}
