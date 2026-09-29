export default function(pi) {
  let gitBranch;
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setFooter((_tui, _theme, data) => {
      gitBranch = data.getGitBranch();
      return {render: () => ["USAGE READY"], invalidate() {}};
    });
  });
  pi.registerProvider("usage-probe", {
    api: "openai-completions", baseUrl: "http://127.0.0.1:1", apiKey: "unused",
    models: [0, 128000].map(contextWindow => ({
      id: String(contextWindow), name: String(contextWindow), contextWindow, maxTokens: 100,
      reasoning: false, input: ["text"], cost: {input:0, output:0, cacheRead:0, cacheWrite:0},
    })),
  });
  pi.registerCommand("usage-state", {handler: async (_args, ctx) => {
    const values = [ctx.getContextUsage() ?? null];
    for (const id of ["128000", "0", "128000"]) {
      if (!await pi.setModel(ctx.modelRegistry.find("usage-probe", id))) throw new Error("model selection failed");
      values.push(ctx.getContextUsage() ?? null);
    }
    ctx.ui.notify("USAGE TRACE " + JSON.stringify(values) + " branch=" + String(gitBranch) + " END USAGE", "info");
  }});
}
