// A ui.custom component in the editor slot (no overlay) that keeps a themed row until invalidate(), as Pi's Text and Box keep theirs.
// The row shows the kept segment, a segment colored at every render, and how often the TUI called invalidate().
export default function (pi) {
  pi.on("session_start", (_event, ctx) => ctx.ui.setStatus("edinv-ready", "EDINV READY"));
  pi.registerCommand("edinv", {
    description: "Open an editor-slot component that keeps a themed row until invalidate()",
    handler: async (_args, ctx) => {
      await ctx.ui.custom((_tui, theme, _keys, done) => {
        let cached;
        let inv = 0;
        return {
          render(_width) {
            if (!cached) cached = theme.fg("accent", "CACHED");
            return [`EDINV ${cached} ${theme.fg("accent", "LIVE")} inv=${inv} EDEND`];
          },
          invalidate() { inv++; cached = undefined; },
          handleInput(data) {
            if (data === "q") done();
            else if (data === "t") ctx.ui.setTheme("light");
          },
        };
      });
    },
  });
}
