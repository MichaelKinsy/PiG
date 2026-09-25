// Reports the ui getters the Node runtime answers synchronously. They used to
// return hardcoded constants because a sync method cannot await a host call.
export default function (pi) {
  pi.registerCommand("report_ui", {
    description: "Report replicated ui state",
    handler: async (_args, ctx) => {
      const themes = ctx.ui.getAllThemes().map((t) => t.name).join(",");
      const named = ctx.ui.getTheme("solarized");
      const spo = ctx.getSystemPromptOptions();
      ctx.ui.notify(
        `text=${JSON.stringify(ctx.ui.getEditorText())}` +
          ` expanded=${ctx.ui.getToolsExpanded()}` +
          ` themes=[${themes}]` +
          ` named=${named ? named.name : "none"}`+
          ` spo_cwd=${spo.cwd ?? "none"}`,
        "info",
      );
    },
  });
}
