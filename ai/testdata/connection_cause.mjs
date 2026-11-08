// Drive the installed pi-ai against a closed local port: error-body.test.ts "surfaces the reason from a real openai SDK connection failure",
// then the same failure through the three streaming APIs whose SDKs hide the cause. argv: expected package version, port.
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const port = Number(process.argv[3]);
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { normalizeProviderError, formatProviderError } = await import(pathToFileURL(dist + "utils/error-body.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
const OpenAI = (await import(pathToFileURL(createRequire(dist + "index.js").resolve("openai")))).default;
const out = {};
const client = new OpenAI({ apiKey: "test", baseURL: `http://127.0.0.1:${port}/v1`, maxRetries: 0 });
const error = await client.chat.completions.create({ model: "m", messages: [{ role: "user", content: "hi" }] }).catch((e) => e);
out.sdk = formatProviderError(normalizeProviderError(error));
for (const [api, file, fn, base] of [["openai-completions", "openai-completions", "openAICompletionsApi", "/v1"], ["openai-responses", "openai-responses", "openAIResponsesApi", "/v1"], ["anthropic-messages", "anthropic-messages", "anthropicMessagesApi", ""]]) {
  const mod = await import(pathToFileURL(dist + "api/" + file + ".lazy.js"));
  const model = { id: "m", name: "m", api, provider: "acme", baseUrl: `http://127.0.0.1:${port}${base}`, reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
  const s = mod[fn]().stream(model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), { apiKey: "k" });
  for await (const e of s) {}
  const m = await s.result();
  out[api] = { stop: m.stopReason, error: m.errorMessage };
}
process.stdout.write(JSON.stringify(out));
