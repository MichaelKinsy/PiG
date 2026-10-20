export default function (pi) {
  pi.registerTool({
    name: "dialog_pick",
    label: "dialog_pick",
    description: "Ask the user to pick a letter in a select dialog.",
    parameters: { type: "object", properties: { title: { type: "string" } }, required: ["title"] },
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const picked = await ctx.ui.select(params.title, ["a", "b", "c", "d"]);
      return { content: [{ type: "text", text: `${params.title}=${picked}` }], details: {} };
    },
  });
}
