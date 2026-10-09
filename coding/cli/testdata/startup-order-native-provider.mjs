// Registers a native Provider object at factory time whose auth check appends "check" to $ORDER_LOG. Pi 0.87.1 queues extension provider registrations while it loads extensions and runs their refresh only after every factory has finished (agent-session-services.ts:158-182).
import { appendFileSync } from "node:fs";

export default function (pi) {
  const models = [{
    id: "order-model", name: "Order model", provider: "order-prov",
    api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false,
    input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 4000, maxTokens: 100,
  }];
  pi.registerProvider({
    id: "order-prov", name: "Order Prov", baseUrl: "http://127.0.0.1:9",
    auth: { apiKey: { name: "Order key", check: async () => { appendFileSync(process.env.ORDER_LOG, "check\n"); return { type: "api_key", source: "order fixture" }; }, resolve: async () => ({ auth: { apiKey: "order" } }) } },
    getModels: () => models,
    stream: () => { throw new Error("unused"); },
    streamSimple: () => { throw new Error("unused"); },
  });
}
