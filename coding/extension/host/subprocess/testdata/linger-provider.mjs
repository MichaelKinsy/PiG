// A provider whose answer names the load of the closure that serves it: only the generation that registered it can say "load 1".
export default function (pi) {
  // The process outlives a reload and runs the factory again (Pi's loader does the same), so the counter names the factory run that made each closure.
  const load = (globalThis.__lingerLoads = (globalThis.__lingerLoads ?? 0) + 1);
  pi.registerProvider("linger-provider", {
    api: "linger-api",
    baseUrl: "https://linger.invalid",
    apiKey: "linger-key",
    models: [{ id: "m", name: "M", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096 }],
    streamSimple: async (model) => {
      const text = `served by closure of load ${load} in process ${process.pid}`;
      const message = { role: "assistant", api: model.api, provider: model.provider, model: model.id, content: [{ type: "text", text }], usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 1 };
      return {
        async *[Symbol.asyncIterator]() {
          yield { type: "start", partial: message };
          yield { type: "text_start", contentIndex: 0, partial: message };
          yield { type: "text_delta", contentIndex: 0, delta: text, partial: message };
          yield { type: "text_end", contentIndex: 0, content: text, partial: message };
          yield { type: "done", reason: "stop", message };
        },
        result: async () => message,
      };
    },
  });
  // A command, a tool and a session handler of the removed extension must go with it.
  pi.registerCommand("linger-command", { description: "removed with its extension", handler: async () => {} });
}
