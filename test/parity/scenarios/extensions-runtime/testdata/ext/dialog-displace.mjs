// Pi 1.2.0 interactive-mode.ts:2690-2857: a dialog that opens while another is shown takes the editor slot and the keys;
// the displaced dialog stays pending. The command opens a selector without waiting for it, then an input.
export default function (pi) {
  pi.registerCommand("dialog-displace", {
    description: "Open a selector, then an input that displaces it",
    handler: async (_args, ctx) => {
      ctx.ui.select("Displaced selector", ["one", "two"]).then(
        (value) => ctx.ui.notify(`displaced-resolved:${value}`, "info"),
        (error) => ctx.ui.notify(`displaced-rejected:${error.message}`, "error"),
      );
      try {
        const value = await ctx.ui.input("Displacing input", "type here");
        ctx.ui.notify(`displacing-result:${value}`, "info");
      } catch (error) {
        ctx.ui.notify(`displacing-rejected:${error.message}`, "error");
      }
    },
  });
}
