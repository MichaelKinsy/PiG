export default function (pi) {
  pi.registerCommand("overflow-widget-probe", {
    description: "Render a widget row wider than the terminal",
    handler: async (_args, ctx) => {
      ctx.ui.setWidget("overflow-probe", () => ({
        render(width) {
          return [`overflow-widget:${"x".repeat(width + 10)}`];
        },
        invalidate() {},
      }));
    },
  });
}
