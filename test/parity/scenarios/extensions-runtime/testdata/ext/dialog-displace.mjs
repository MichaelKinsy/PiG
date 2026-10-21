// Pi bdd9f039 interactive-mode.ts pushExtensionDialog (#11166): a dialog that opens while another is shown hides it without settling it; closing the
// shown dialog shows the earlier one again. The command opens a selector without waiting for it, then an input, answers the input, and the selector
// comes back and settles with its own answer. One notification reports both answers (consecutive status lines replace one another).
export default function (pi) {
  pi.registerCommand("dialog-displace", {
    description: "Open a selector, then an input that stacks over it",
    handler: async (_args, ctx) => {
      const earlier = ctx.ui.select("Displaced selector", ["one", "two"]).then(
        (value) => `displaced=${value}`,
        (error) => `displaced-rejected:${error.message}`,
      );
      let later;
      try {
        later = `displacing=${await ctx.ui.input("Displacing input", "type here")}`;
      } catch (error) {
        later = `displacing-rejected:${error.message}`;
      }
      ctx.ui.notify(`dialog-stack-result:${later} ${await earlier}`, "info");
    },
  });
}
