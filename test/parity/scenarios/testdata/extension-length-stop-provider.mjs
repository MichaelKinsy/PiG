// A provider whose every answer stops for length after five output tokens, far below its model's output limit. Pi treats such a stop as
// recoverable only when the model has an output limit (overflow.ts isRecoverableLength: `desiredMaxOutput > 0 && usage.output <
// desiredMaxOutput`), so what print mode does with it shows whether the session's model kept its limits.
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";

export default function (pi) {
  pi.registerProvider("length-stop", {
    baseUrl: "http://127.0.0.1:9/v1",
    apiKey: "unused",
    api: "length-stop-api",
    models: [
      { id: "length-1", name: "Length Stop", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 64000, maxTokens: 2048 },
    ],
    streamSimple: (model) => {
      const stream = createAssistantMessageEventStream();
      const message = {
        role: "assistant", content: [], api: model.api, provider: model.provider, model: model.id,
        usage: { input: 10, output: 5, cacheRead: 0, cacheWrite: 0, totalTokens: 15, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
        stopReason: "stop", timestamp: 123,
      };
      queueMicrotask(() => {
        stream.push({ type: "start", partial: structuredClone(message) });
        message.content.push({ type: "text", text: "partial answer" });
        stream.push({ type: "text_start", contentIndex: 0, partial: message });
        stream.push({ type: "text_delta", contentIndex: 0, delta: "partial answer", partial: message });
        stream.push({ type: "text_end", contentIndex: 0, content: "partial answer", partial: message });
        message.stopReason = "length";
        stream.push({ type: "done", reason: "length", message });
        stream.end();
      });
      return stream;
    },
  });
}
