// Registers an extension provider whose models carry a window, and a /pick command that selects Model B with pi.setModel. The widget is set once at session start and never again, so
// nothing but the selection itself can redraw the footer.
export default function (pi) {
  const cost = { input: 3.25, output: 16.5, cacheRead: 0.325, cacheWrite: 4.0625 };
  pi.registerProvider("metaprov", {
    api: "openai-completions",
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "probe-key",
    models: [
      { id: "model-a", name: "Model A", reasoning: true, input: ["text", "image"], cost, contextWindow: 777000, maxTokens: 55555 },
      { id: "model-b", name: "Model B", reasoning: true, input: ["text"], cost, contextWindow: 555000, maxTokens: 44444 },
    ],
  });
  pi.registerCommand("pick", {
    description: "Select Model B with pi.setModel",
    handler: async (_args, ctx) => {
      await pi.setModel(ctx.modelRegistry.find("metaprov", "model-b"));
    },
  });
  pi.on("session_start", (_event, ctx) => {
    if (ctx.hasUI) ctx.ui.setWidget("set-model-footer", ["SET MODEL FOOTER PROBE"]);
  });
}
