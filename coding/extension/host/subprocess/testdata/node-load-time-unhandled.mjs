import { completeSimple } from "@earendil-works/pi-ai";

// A host with nothing to serve the model stream must answer the call with an error, not leave the module waiting for it.
const model = { id: "m", name: "m", api: "openai-completions", provider: "probe-provider", baseUrl: "http://127.0.0.1:9/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const answer = await completeSimple(model, { messages: [{ role: "user", content: "ping", timestamp: 1 }] }, { apiKey: "k" });

export default function (pi) {
  pi.registerCommand("load_time_unhandled", {
    description: "report the answer to a call the host has no handler for",
    handler: async () => {
      if (answer.stopReason !== "error" || !String(answer.errorMessage).includes("the host has no handler for modelStream")) throw new Error(JSON.stringify(answer));
    },
  });
}
