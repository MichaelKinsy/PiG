export default function (pi) {
  pi.registerTool({
    name: "linger_overlay_tool",
    label: "Linger Overlay Tool",
    description: "Open a custom overlay from a tool that stays open until q/Esc",
    parameters: { type: "object", properties: {} },
    async execute(_toolCallId, _params, _signal, _onUpdate, ctx) {
      const result = await ctx.ui.custom((tui, _theme, _kb, done) => ({
        render(width) {
          return [`LINGER-TOOL width=${width}`];
        },
        handleInput(data) {
          if (data === "q" || data === "Q" || data === "\u001b") {
            done("closed");
          }
        },
        invalidate() {},
        dispose() {},
      }));
      return { output: `linger-tool-result:${String(result)}` };
    },
  });
}
