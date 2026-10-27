// register-valid-provider: an extension that registers one provider the model runtime accepts, with a configured key, so a second provider is available.
export default function (pi) {
  pi.registerProvider("valid-provider", {
    api: "openai-completions",
    baseUrl: "https://valid.invalid/v1",
    apiKey: "key",
    models: [{ id: "valid-model", name: "Valid", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4096, maxTokens: 1024 }],
  });
}
