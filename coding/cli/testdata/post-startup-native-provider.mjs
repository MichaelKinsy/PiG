// Registers a native Provider object from a command handler, after startup. Pi 0.87.1 starts `void refresh({ allowNetwork: false })` from registerProvider (model-runtime.ts:744-750); that refresh lands on a later event-loop turn without another model-runtime call, so a later RPC get_available_models lists the model (rpc-mode.ts:473).
export default function (pi) {
  const models = [{
    id: "post-startup-model", name: "Post startup model", provider: "post-startup-prov",
    api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false,
    input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 4000, maxTokens: 100,
  }];
  pi.registerCommand("register_native", {
    description: "Register the post-startup native Provider",
    handler: async () => {
      pi.registerProvider({
        id: "post-startup-prov", name: "Post Startup Prov", baseUrl: "http://127.0.0.1:9",
        auth: { apiKey: { name: "Post startup key", check: async () => ({ type: "api_key", source: "post startup fixture" }), resolve: async () => ({ auth: { apiKey: "post-startup" } }) } },
        getModels: () => models,
        stream: () => { throw new Error("unused"); },
        streamSimple: () => { throw new Error("unused"); },
      });
    },
  });
}
