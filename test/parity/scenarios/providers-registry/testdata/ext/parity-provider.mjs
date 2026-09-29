// parity-provider: registers a model provider so --list-models exposes it.
// Both pig (-e) and pi (-e) load this extension; the extension-registered
// provider must appear in --list-models on both, proving that extension
// providers are registered before the model catalog is listed.
export default function (pi) {
  pi.registerProvider("parity-prov", {
    name: "Parity Prov",
    baseUrl: "http://127.0.0.1:9",
    apiKey: "x",
    api: "openai-completions",
    models: [{
      id: "parity-model",
      name: "parity-model",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 1000,
      maxTokens: 100,
    }],
  });
}
