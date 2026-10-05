import { AsyncResource } from "node:async_hooks";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

// Module evaluation runs outside any Host request, so this scope has no ambient request.
const outsideRequests = new AsyncResource("outside-requests");

const model = {
  id: "native-model", name: "Native Model", api: "openai-completions", provider: "native-probe",
  baseUrl: "http://127.0.0.1:9/v1", reasoning: false, input: ["text"],
  cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100,
};

// Pi's ModelRuntime.registerNativeProvider installs this provider in the catalog, and a
// caller's StreamOptions.onPayload reaches the Provider as a callback the Host declared
// for that one provider call (provider-composer.ts:361-366). A real Provider runs its
// transport in its own chain, so it invokes that callback while the extension is
// handling another Host request. The callback still belongs to the provider call that
// declared it.
export default function (pi) {
  let invokePayload = null;
  let finishPayload = null;
  let payloadError = null;

  pi.registerCommand("payload_probe", {
    description: "Drive the pending provider callback from this command's request",
    handler: async args => {
      if (!invokePayload) throw new Error("no provider callback is pending");
      try {
        const call = () => invokePayload({ marker: "payload" });
        await (args === "detached" ? outsideRequests.runInAsyncScope(call) : call());
      } catch (error) {
        payloadError = error;
      }
      finishPayload();
    },
  });

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
      if (typeof options.onPayload !== "function") throw new Error("the Host declared no onPayload callback");
      const stream = createAssistantMessageEventStream();
      const message = {
        role: "assistant", content: [], api: requested.api, provider: requested.provider, model: requested.id,
        usage: { input: 1, output: 2, cacheRead: 0, cacheWrite: 0, totalTokens: 3, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
        stopReason: "stop", timestamp: 123,
      };
      invokePayload = value => options.onPayload(value, requested);
      const awaited = new Promise(resolve => { finishPayload = resolve; });
      queueMicrotask(() => {
        stream.push({ type: "start", partial: { ...message } });
        // The provider's request work continues after the stream is returned, and the
        // Host-declared payload callback runs inside the command request, which is not
        // the provider call that declared it.
        void awaited.then(() => {
          if (payloadError) {
            stream.push({ type: "error", reason: "error", error: { ...message, content: [], stopReason: "error", errorMessage: String(payloadError?.message ?? payloadError) } });
            return;
          }
          message.content.push({ type: "text", text: "native answer" });
          stream.push({ type: "text_start", contentIndex: 0, partial: { ...message } });
          stream.push({ type: "text_delta", contentIndex: 0, delta: "native answer", partial: { ...message } });
          stream.push({ type: "text_end", contentIndex: 0, content: "native answer", partial: { ...message } });
          stream.push({ type: "done", reason: "stop", message });
        });
      });
      return stream;
    },
  });
}
