import { completeSimple } from "@earendil-works/pi-ai";

// Pi runs pi-ai in the extension's own process with no registration gate, so a module can call it at top level or in its factory before it registers anything.
const model = { id: "m", name: "m", api: "openai-completions", provider: "probe-provider", baseUrl: "http://127.0.0.1:9/v1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
const ask = () => completeSimple(model, { messages: [{ role: "user", content: "ping", timestamp: Date.now() }] }, { apiKey: "k" });
const atTopLevel = await ask();

export default async function (pi) {
  const inFactory = await ask();
  pi.registerCommand("load_time_completion", {
    description: "report what the load-time completions answered",
    handler: async () => {
      for (const [where, result] of [["top level", atTopLevel], ["factory", inFactory]]) {
        const text = result.content?.map((block) => block.text).join("");
        if (result.stopReason !== "stop" || text !== "pong") throw new Error(`${where}: ${JSON.stringify(result)}`);
      }
    },
  });
}
