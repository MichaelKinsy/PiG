// reload-provider-failure: an .mjs extension whose provider registration is valid on its first load and rejected by the model runtime
// on every later load (the module stays in Node's ESM cache across /reload, so the counter keeps counting). It shows how many times
// its factory ran as a widget above the editor, which only a loaded extension can set.
let factoryCalls = 0;

export default function (pi) {
  factoryCalls++;
  const calls = factoryCalls;
  const model = { id: "reload-model", name: "Reload", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4096, maxTokens: 1024 };
  pi.registerProvider("reload-provider", {
    baseUrl: "https://reload.invalid/v1",
    apiKey: "key",
    // The first load names an api; the reload leaves it out, which the model runtime rejects.
    ...(calls === 1 ? { api: "openai-completions" } : {}),
    models: [model],
  });
  pi.on("session_start", (_event, ctx) => ctx.ui.setWidget("reload-provider", [`reload-provider-calls=${calls}`]));
}
