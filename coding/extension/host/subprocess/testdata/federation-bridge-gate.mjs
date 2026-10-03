import { completeSimple } from "@earendil-works/pi-ai/compat";

// The bridge gate for anthropic-messages mirrors anthropic-messages.ts:614-615, 932-937: federation replaces the
// request-auth assertion only for provider "anthropic" with the rule, organization and identity token file set in
// options.env or the process env; otherwise "No API key for provider: <provider>".
const complete = async (provider, env, label) => {
  const model = ctxModel(provider);
  const result = await completeSimple(model, { messages: [{ role: "user", content: "hello" }] }, env ? { env } : {});
  return `${label}: ${result.stopReason}: ${result.errorMessage ?? ""}`;
};
let registry;
const ctxModel = (provider) => {
  const model = registry.find(provider, "claude-test");
  if (!model) throw new Error(`model not found: ${provider}`);
  return model;
};
const full = { ANTHROPIC_FEDERATION_RULE_ID: "fdrl_test", ANTHROPIC_ORGANIZATION_ID: "org-test", ANTHROPIC_IDENTITY_TOKEN_FILE: "/nonexistent/identity.jwt" };

export default function (pi) {
  const register = (name, run) =>
    pi.registerCommand(name, {
      description: name,
      handler: async (_args, ctx) => {
        registry = ctx.modelRegistry;
        const outcome = await run();
        if (outcome) throw new Error(outcome);
      },
    });
  const expectRejected = (provider, env, label) => async () => {
    const text = await complete(provider, env, label);
    return text === `${label}: error: No API key for provider: ${provider}` ? undefined : text;
  };
  register("federation_incomplete", expectRejected("anthropic", { ANTHROPIC_FEDERATION_RULE_ID: "fdrl_test", ANTHROPIC_IDENTITY_TOKEN_FILE: "/x" }, "incomplete"));
  register("federation_empty_value", expectRejected("anthropic", { ...full, ANTHROPIC_ORGANIZATION_ID: "" }, "empty"));
  register("federation_other_provider", expectRejected("minimax", full, "other-provider"));
  register("federation_no_env", expectRejected("anthropic", undefined, "no-env"));
  register("federation_process_env", async () => {
    const text = await complete("anthropic", undefined, "process-env");
    return text === "process-env: stop: " ? undefined : text;
  });
}
