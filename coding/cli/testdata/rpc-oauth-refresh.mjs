// A native Provider with an OAuth method whose token endpoint is a fake: refresh() writes RPC_OAUTH_REFRESH_STARTED, then answers after RPC_OAUTH_REFRESH_MS with a rotated token. The catalog refresh of RPC mode refreshes an expired credential of this provider (refreshModels is declared, as for the built-in catalogs), so the token request is in flight when the test ends the process (Pi main b2cb62923, #11692).
import { writeFileSync } from "node:fs";

export default function (pi) {
  const models = [{
    id: "rotating-model", name: "Rotating model", provider: "rotating-oauth",
    api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false,
    input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow: 4000, maxTokens: 100,
  }];
  pi.registerProvider({
    id: "rotating-oauth", name: "Rotating OAuth", baseUrl: "http://127.0.0.1:9",
    auth: {
      oauth: {
        name: "Rotating subscription",
        login: async () => { throw new Error("unused"); },
        refresh: async () => {
          writeFileSync(process.env.RPC_OAUTH_REFRESH_STARTED, "started");
          await new Promise((resolve) => setTimeout(resolve, Number(process.env.RPC_OAUTH_REFRESH_MS || "400")));
          return { type: "oauth", access: "new-access", refresh: "new-refresh", expires: Date.now() + 3_600_000 };
        },
        toAuth: async (credential) => ({ apiKey: credential.access }),
      },
    },
    getModels: () => models,
    refreshModels: async () => {},
    stream: () => { throw new Error("unused"); },
    streamSimple: () => { throw new Error("unused"); },
  });
  // A request-time refresh that nobody awaits when the run ends: the extension asks for the provider's credentials at session start and goes on, as a request that its caller abandoned does.
  if (process.env.RPC_OAUTH_REFRESH_ON_SESSION_START) {
    pi.on("session_start", (_event, ctx) => {
      const model = ctx.modelRegistry.find("rotating-oauth", "rotating-model");
      void ctx.modelRegistry.getApiKeyAndHeaders(model).catch(() => {});
    });
  }
}
