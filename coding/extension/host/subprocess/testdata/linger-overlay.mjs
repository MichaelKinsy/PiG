export default function (pi) {
  pi.registerCommand("linger_overlay", {
    description: "Open a custom overlay that stays open until q/Esc",
    async handler(_args, ctx) {
      const result = await ctx.ui.custom((tui, _theme, _kb, done) => ({
        render(width) {
          return [`LINGER width=${width}`];
        },
        handleInput(data) {
          if (data === "q" || data === "Q" || data === "\u001b") {
            done("closed");
          }
        },
        invalidate() {},
        dispose() {},
      }));
      ctx.ui.notify(`linger-result:${String(result)}`, "info");
    },
  });
}
