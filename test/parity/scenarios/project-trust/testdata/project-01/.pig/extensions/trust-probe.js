// trust-probe: an auto-discovered project extension that registers a provider.
// It must execute only when the project is trusted, so both the provider name
// and the model id are asserted absent in the deny scenarios and present in the
// approve scenarios.
export default function (pi) {
  pi.registerProvider("trust-probe", {
    name: "Trust Probe",
    baseUrl: "http://127.0.0.1:9",
    apiKey: "x",
    api: "openai-completions",
    models: [{
      id: "must-not-load-before-trust",
      name: "must-not-load-before-trust",
      reasoning: false,
      input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 1000,
      maxTokens: 100,
    }],
  });
}
