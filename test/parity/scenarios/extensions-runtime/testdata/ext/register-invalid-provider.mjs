// register-invalid-provider: an extension that registers one provider the model
// runtime rejects (a model with no "api") and keeps loading. Pi queues the
// registration and reports the rejection when the services apply it.
export default function (pi) {
  pi.registerProvider("bad-provider", {
    baseUrl: "https://bad.invalid/v1",
    apiKey: "key",
    models: [{ id: "bad-model", name: "Bad", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4096, maxTokens: 1024 }],
  });
}
