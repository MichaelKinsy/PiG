// Registers a model provider at factory time, as Pi's startup model resolution and --list-models must observe it.
export default function (pi) {
  pi.registerProvider("startup-prov", {
    name: "Startup Prov",
    baseUrl: "http://127.0.0.1:9",
    apiKey: "startup-fixture",
    api: "openai-completions",
    models: [{
      id: "startup-model",
      name: "Startup model",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 2048,
      maxTokens: 128,
    }],
  });
}
