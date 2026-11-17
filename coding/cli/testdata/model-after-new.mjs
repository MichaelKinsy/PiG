// model-after-new.mjs registers an extension provider whose models carry metadata no default produces (a window, an output limit, costs, reasoning and an
// image input) over the server PIG_TEST_MODEL_AFTER_NEW_URL names, and records the model each model_select event carries and the thinking level the extension
// reads then, as lines of PIG_TEST_REPLACE_LOG. PIG_TEST_MODEL_AFTER_NEW_SHAPE=oauth registers the provider with an OAuth login in place of the API key. Every
// Session start runs the factory again in a fresh process, so the provider is registered once per Session.
import { appendFileSync } from "node:fs";

const log = (record) => appendFileSync(process.env.PIG_TEST_REPLACE_LOG, JSON.stringify(record) + "\n");
const describe = (model) => model && { id: `${model.provider}/${model.id}`, name: model.name, contextWindow: model.contextWindow, maxTokens: model.maxTokens, reasoning: model.reasoning, input: model.input, cost: model.cost };
const cost = { input: 3.25, output: 16.5, cacheRead: 0.325, cacheWrite: 4.0625 };

export default function modelAfterNew(pi) {
  const auth =
    process.env.PIG_TEST_MODEL_AFTER_NEW_SHAPE === "oauth"
      ? {
          oauth: {
            name: "Meta provider",
            login: async () => ({ refresh: "r", access: "a", expires: 4102444800000 }),
            refreshToken: async (credentials) => credentials,
            getApiKey: (credentials) => credentials.access,
          },
        }
      : { apiKey: "probe-key" };
  pi.registerProvider("metaprov", {
    api: "openai-completions",
    baseUrl: process.env.PIG_TEST_MODEL_AFTER_NEW_URL,
    ...auth,
    models: [
      { id: "model-a", name: "Model A", reasoning: true, input: ["text", "image"], cost, contextWindow: 777000, maxTokens: 55555 },
      { id: "model-b", name: "Model B", reasoning: true, input: ["text"], cost, contextWindow: 555000, maxTokens: 44444 },
    ],
  });
  // /pick selects Model B through the extension API: the model comes from the registry the extension sees, and setModel takes it from there.
  pi.registerCommand("pick", {
    description: "Select Model B with pi.setModel",
    handler: async (_args, ctx) => {
      await pi.setModel(ctx.modelRegistry.find("metaprov", "model-b"));
    },
  });
  pi.on("session_start", (event) => log({ event: "session_start", reason: event.reason }));
  pi.on("model_select", (event) => log({ event: "model_select", source: event.source, model: describe(event.model), thinking: pi.getThinkingLevel() }));
}
