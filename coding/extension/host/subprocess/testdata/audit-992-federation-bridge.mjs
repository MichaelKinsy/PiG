import { completeSimple } from "@earendil-works/pi-ai/compat";

// Pi 0.99.2: an anthropic request with no apiKey and no auth header federates when options.env carries the
// workload identity federation variables (anthropic-messages.ts:614-615, 932-937), so compat completeSimple sends it.
export default function (pi) {
  pi.registerCommand("federation_complete", {
    description: "compat completion authenticated only by workload identity federation env",
    handler: async (_args, ctx) => {
      const model = ctx.modelRegistry.find("anthropic", "claude-test");
      if (!model) throw new Error("model not found");
      const env = { ANTHROPIC_FEDERATION_RULE_ID: "fdrl_test", ANTHROPIC_ORGANIZATION_ID: "org-test", ANTHROPIC_IDENTITY_TOKEN_FILE: "/nonexistent/identity.jwt" };
      const result = await completeSimple(model, { messages: [{ role: "user", content: "hello" }] }, { env });
      if (result.stopReason !== "stop") throw new Error(`result = ${JSON.stringify(result)}`);
    },
  });
}
