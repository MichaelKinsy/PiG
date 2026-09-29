export default function (pi) {
  pi.registerCommand("rigidity-themes", { handler: async (_, ctx) => {
    const loaded = ctx.ui.getTheme("light");
    const rows = [
      `lookup:${loaded?.name}:${typeof loaded?.fg}:${ctx.ui.theme.name}`,
      `bold:${JSON.stringify(loaded?.bold?.("x"))}`,
      `missing:${ctx.ui.getTheme("rigidity-missing") === undefined}`,
      `set-light:${JSON.stringify(ctx.ui.setTheme("light"))}`,
      `active:${ctx.ui.theme.name}`,
      `set-missing:${JSON.stringify(ctx.ui.setTheme("rigidity-missing"))}`,
      `fallback:${ctx.ui.theme.name}`,
    ];
    await ctx.ui.select("Theme results", rows);
  }});
}
