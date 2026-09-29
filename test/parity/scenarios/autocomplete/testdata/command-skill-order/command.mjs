export default function (pi) {
  pi.registerCommand("btw", {
    description: "Side-question command",
    handler: async (_args, ctx) => {
      ctx.ui.notify("AUTOCOMPLETE COMMAND SELECTED", "info");
    },
  });
}
