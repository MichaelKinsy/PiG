// Drive the installed pi-ai stream/streamSimple of an API kind and report the body its client hands to fetch, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let captured = null;
// Each client's default fetch is the global one at the time of the call; the Mistral client hands it a Request. The 400 ends the stream with the first request.
globalThis.fetch = async (input, init) => {
  if (captured === null) {
    if (init?.body != null) captured = typeof init.body === "string" ? init.body : await new Response(init.body).text();
    else if (typeof input === "object" && typeof input.clone === "function") captured = await input.clone().text();
  }
  return new Response('{"error":{"type":"invalid_request_error","message":"captured","code":400,"status":"INVALID_ARGUMENT"}}', { status: 400, headers: { "content-type": "application/json" } });
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const row of JSON.parse(input)) {
  const api = await import(pathToFileURL(dist + "api/" + row.api + ".js"));
  const model = { id: row.id, name: row.id, api: row.api, provider: row.provider, baseUrl: row.baseUrl, reasoning: row.reasoning, input: ["text", "image"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 16384, ...(row.compat ? { compat: row.compat } : {}), ...(row.model ?? {}) };
  captured = null;
  const run = row.simple ? api.streamSimple : api.stream;
  let result;
  try {
    result = await run(model, normalizeContext(row.context), { ...row.options, apiKey: row.apiKey, maxRetries: 0 }).result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  // A stream that ends before any request reports how it ended.
  results.push(captured === null ? "no request: " + (result.errorMessage ?? result.stopReason) : captured);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
