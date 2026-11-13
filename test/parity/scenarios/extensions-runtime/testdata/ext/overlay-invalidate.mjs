// A ui.custom overlay whose component keeps a themed row until invalidate(), as Pi's Text and Box keep theirs. The row shows the
// kept segment, a segment colored at every render, and how often the TUI called invalidate().
export default function (pi) {
  pi.on("session_start", (_event, ctx) => ctx.ui.setStatus("ovinv-ready", "OVINV READY"));
  const open = (nonCapturing) => async (_args, ctx) => {
    await ctx.ui.custom((_tui, theme, _keys, done) => {
      let cached;
      let inv = 0;
      return {
        render(_width) {
          if (!cached) cached = theme.fg("accent", "CACHED");
          return [`OVINV ${cached} ${theme.fg("accent", "LIVE")} inv=${inv} OVEND`];
        },
        invalidate() { inv++; cached = undefined; },
        handleInput(data) {
          if (data === "q") done();
          else if (data === "t") ctx.ui.setTheme("light");
        },
      };
    }, { overlay: true, overlayOptions: { width: 60, anchor: "top-left", row: 0, col: 0, nonCapturing } });
  };
  pi.registerCommand("ovinv", { description: "Open a focused overlay that keeps a themed row until invalidate()", handler: open(false) });
  pi.registerCommand("ovinvnc", { description: "Open the same overlay without taking focus", handler: open(true) });
}
