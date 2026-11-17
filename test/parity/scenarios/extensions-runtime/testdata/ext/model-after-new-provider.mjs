// Registers an extension provider whose models carry a window, an output limit, costs, an image input and reasoning, and lists in a widget what each model_select event carried and
// the thinking level the extension reads then. Every Session start runs the factory again, so the widget after /new lists only that Session's events.
export default function (pi) {
  const lines = ["MODEL AFTER NEW PROBE"];
  const show = (ctx) => ctx.hasUI && ctx.ui.setWidget("model-after-new", [...lines]);
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
  pi.on("session_start", (event, ctx) => {
    lines.push(`session_start ${event.reason}`);
    show(ctx);
  });
  pi.on("model_select", (event, ctx) => {
    const model = event.model;
    lines.push(`model_select ${event.source} ${model.provider}/${model.id} window=${model.contextWindow} max=${model.maxTokens} reasoning=${model.reasoning} image=${model.input.includes("image")} cost=${model.cost.input}/${model.cost.output}/${model.cost.cacheRead}/${model.cost.cacheWrite} thinking=${pi.getThinkingLevel()}`);
    show(ctx);
  });
}
