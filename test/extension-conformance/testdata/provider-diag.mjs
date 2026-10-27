const model = id => [{ id, name: id, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4096, maxTokens: 1024 }];
export default function (pi) {
  pi.registerProvider("diag-good", { api: "openai-completions", baseUrl: "https://good.invalid/v1", apiKey: "key", models: model("good-model") });
  pi.registerProvider("diag-bad", { baseUrl: "https://bad.invalid/v1", apiKey: "key", models: model("bad-model") });
  pi.registerTool({ name: "diag_ping", label: "diag_ping", description: "diag_ping", parameters: { type: "object" }, execute: async () => ({ content: [{ type: "text", text: "pong" }], details: {} }) });
}
