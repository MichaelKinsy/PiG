// Registers a native Provider object at factory time. Pi 0.87.1 registers it with no auth check (model-runtime.ts:744-750); createAgentSessionServices then awaits refresh({ allowNetwork: false }) (agent-session-services.ts:182), which checks its auth before startup model resolution and --list-models.
export default function (pi) {
  const models = [{
    id: "native-startup-model", name: "Native startup model", provider: "native-startup-prov",
    api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false,
    input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 4000, maxTokens: 100,
  }];
  pi.registerProvider({
    id: "native-startup-prov", name: "Native Startup Prov", baseUrl: "http://127.0.0.1:9",
    auth: { apiKey: { name: "Native startup key", check: async () => ({ type: "api_key", source: "native startup fixture" }), resolve: async () => ({ auth: { apiKey: "native-startup" } }) } },
    getModels: () => models,
    stream: () => { throw new Error("unused"); },
    streamSimple: () => { throw new Error("unused"); },
  });
}
