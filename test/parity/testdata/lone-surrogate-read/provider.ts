// A provider an extension registers by configuration; its model's strings hold a lone UTF-16 surrogate.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const U = "\ud83d";

export default function (pi: ExtensionAPI) {
  pi.registerProvider("extp", {
    baseUrl: process.env.PROBE_BASE,
    apiKey: "test-key",
    api: "openai-completions",
    models: [{ id: "xm", name: `XN[${U}]`, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 4096,
      compat: { openRouterRouting: { sort: `XS[${U}]`, order: [`XO[${U}]`] } } }],
  });
}
