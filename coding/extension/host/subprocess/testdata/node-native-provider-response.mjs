import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

const model = {
  id: "native-model", name: "Native Model", api: "openai-completions", provider: "native-probe",
  baseUrl: "http://127.0.0.1:9/v1", reasoning: false, input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100,
};

// Pi types.ts:1917-1920: a custom provider's streamSimple "must invoke options.onResponse after receiving the response and before
// consuming its body". The Host declares onResponse for the provider call when the caller passed one (sdk.ts:387-433 handleProviderResponse
// is that caller), and the provider invokes it with the response status and headers before it emits its events. It may also invoke
// options.onProviderStreamEvent with a parsed stream event (types.ts:1918-1919), which sdk.ts handleProviderStreamEvent turns into
// provider_stream_event.
export default function (pi) {
  pi.registerProvider({
    id: "native-probe",
    name: "Native",
    baseUrl: model.baseUrl,
    auth: {
      apiKey: {
        name: "API key",
        check: async () => ({ type: "api_key", source: "native-check" }),
        resolve: async () => ({ auth: { apiKey: "native-key" } }),
      },
    },
    getModels: () => [model],
    streamSimple: (requested, _context, options) => {
      const stream = createAssistantMessageEventStream();
      const message = {
        role: "assistant", content: [], api: requested.api, provider: requested.provider, model: requested.id,
        usage: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0, totalTokens: 3, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
        stopReason: "stop", timestamp: 123,
      };
      void (async () => {
        try {
          if (typeof options.onPayload === "function") await options.onPayload({ marker: "payload" }, requested);
          if (typeof options.onResponse !== "function") throw new Error("the Host declared no onResponse callback");
          await options.onResponse({ status: 201, headers: { "x-probe": "yes" } }, requested);
          if (typeof options.onProviderStreamEvent !== "function") throw new Error("the Host declared no onProviderStreamEvent callback");
          await options.onProviderStreamEvent({ raw: "chunk", index: 1 }, requested);
          stream.push({ type: "start", partial: { ...message } });
          message.content.push({ type: "text", text: "native answer" });
          stream.push({ type: "done", reason: "stop", message });
        } catch (error) {
          stream.push({ type: "error", reason: "error", error: { ...message, content: [], stopReason: "error", errorMessage: String(error?.message ?? error) } });
        }
      })();
      return stream;
    },
  });
}
