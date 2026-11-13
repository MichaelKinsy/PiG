// Drive the installed pi-ai anthropic-messages stream/streamSimple and report the body the Anthropic SDK hands to fetch, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const anthropic = await import(pathToFileURL(dist + "api/anthropic-messages.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let captured = null;
// The SDK's default fetch is the global one at the time of the call. The 400 ends the stream with the first request.
globalThis.fetch = async (_url, init) => {
  captured = typeof init.body === "string" ? init.body : await new Response(init.body).text();
  return new Response('{"type":"error","error":{"type":"invalid_request_error","message":"captured"}}', { status: 400, headers: { "content-type": "application/json" } });
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const row of JSON.parse(input)) {
  const model = { id: "claude-test", name: "claude-test", api: "anthropic-messages", provider: row.provider, baseUrl: row.baseUrl, reasoning: row.reasoning, input: ["text", "image"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 16384, ...(row.compat ? { compat: row.compat } : {}) };
  captured = null;
  const run = row.simple ? anthropic.streamSimple : anthropic.stream;
  let result;
  try {
    result = await run(model, normalizeContext(row.context), { ...row.options, apiKey: row.apiKey, maxRetries: 0 }).result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  results.push(captured === null ? { none: result.errorMessage ?? result.stopReason } : captured);
}
process.stdout.write(JSON.stringify(results));
