export default function (pi: any) {
  pi.registerCommand("rigidity-dialogs", {
    description: "Exercise remapped extension dialogs without a provider request",
    handler: async (_args: string, ctx: any) => {
      const selected = await ctx.ui.select("Rigidity selection", ["", "chosen"]);
      const expanded = ctx.ui.getToolsExpanded();
      const entered = await ctx.ui.input("Rigidity input");
      await ctx.ui.select("Dialog results", [JSON.stringify({ selected, expanded, entered })]);
    },
  });
}
