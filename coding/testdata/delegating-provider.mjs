// A provider whose streamSimple hands its request to pi-ai's compat streamSimple for a stock API, as pi-commandcode-provider does (issue #164). The provider registers its streamSimple either for its own api or for the stock API it delegates to. Pi's compat dispatch resolves the pi-ai API implementation from model.api (packages/ai/src/compat.ts:278-293), so the request reaches the HTTP endpoint instead of this provider's streamSimple. Each registration serves one request; a second call is a re-entry and fails at once instead of recursing without bound.
import { streamSimple } from "@earendil-works/pi-ai/compat";

const ZERO = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };

export default function (pi) {
  pi.registerCommand("register-delegating", {
    description: "register a provider that delegates to pi-ai's stock API",
    handler: async (args) => {
      const [id, api, baseUrl, registeredApi] = args.split(" ");
      let calls = 0;
      pi.registerProvider(id, {
        api: registeredApi, baseUrl, apiKey: "delegating-key",
        models: [{ id: "chat", name: "Chat", reasoning: false, input: ["text"], cost: ZERO, contextWindow: 128000, maxTokens: 4096 }],
        streamSimple: (model, context, options) => {
          calls += 1;
          if (calls > 1) throw new Error(`${id} streamSimple re-entered`);
          return streamSimple({ ...model, api }, context, options);
        },
      });
    },
  });
}
