import { completeSimple } from "@earendil-works/pi-ai/compat";

export default function (pi) {
  pi.registerCommand("model_abort", {
    description: "abort a compat completion mid-request",
    handler: async (_args, ctx) => {
      const model = ctx.modelRegistry.find("openrouter", "org/model/name");
      if (!model) throw new Error("model not found");
      const controller = new AbortController();
      const pending = completeSimple(model, { messages: [{ role: "user", content: "hello" }] }, { apiKey: "request-key", signal: controller.signal });
      setTimeout(() => controller.abort(), 200);
      const result = await pending;
      if (result.stopReason !== "aborted") throw new Error(`result = ${JSON.stringify(result)}`);
    },
  });
}
