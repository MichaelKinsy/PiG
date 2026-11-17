// An extension in the agent directory that registers a provider with its own streamSimple. The answer names the factory run that made the closure and the
// number of the call it serves.
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

globalThis.__removedProviderLoads = (globalThis.__removedProviderLoads ?? 0) + 1;

export default function (pi) {
  const load = globalThis.__removedProviderLoads;
  let calls = 0;
  pi.registerProvider("removed-prov", {
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "unused",
    api: "removed-api",
    models: [{ id: "r1", name: "Removed One", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 64000, maxTokens: 2048 }],
    streamSimple: (model) => {
      const stream = createAssistantMessageEventStream();
      const text = `served by closure of load ${load}, call ${++calls}`;
      const message = {
        role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id,
        usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
        stopReason: "stop", timestamp: 1,
      };
      queueMicrotask(() => {
        stream.push({ type: "start", partial: structuredClone(message) });
        message.content.push({ type: "text", text });
        stream.push({ type: "text_start", contentIndex: 0, partial: message });
        stream.push({ type: "text_delta", contentIndex: 0, delta: text, partial: message });
        stream.push({ type: "text_end", contentIndex: 0, content: text, partial: message });
        stream.push({ type: "done", reason: "stop", message });
        stream.end();
      });
      return stream;
    },
  });
}
