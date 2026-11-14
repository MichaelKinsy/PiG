// Drive the installed pi-ai stream of each row's API kind and report the body the provider's SDK hands to fetch, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
const modules = {};
let captured = null;
// Each SDK's default fetch is the global one at the time of the call: it gets a URL and an init, or (the Mistral SDK) a Request. The 400 ends the
// stream with the first request.
globalThis.fetch = async (input, init) => {
  const body = init?.body ?? (input instanceof Request ? await input.clone().arrayBuffer() : undefined);
  captured = typeof body === "string" ? body : Buffer.from(body instanceof ArrayBuffer ? body : await new Response(body).arrayBuffer()).toString("utf8");
  return new Response('{"type":"error","error":{"type":"invalid_request_error","message":"captured"}}', { status: 400, headers: { "content-type": "application/json" } });
};
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const row of JSON.parse(input)) {
  captured = null;
  let result;
  try {
    const module = (modules[row.api] ??= await import(pathToFileURL(dist + "api/" + row.api + ".js")));
    const model = { id: row.id, name: row.id, api: row.api, provider: row.provider, baseUrl: row.baseUrl, reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: row.contextWindow, maxTokens: 16384,
      ...(row.compat ? { compat: row.compat } : {}) };
    // A hook that hands the payload back: the SDK then posts the payload the hook returned.
    const options = { apiKey: row.apiKey, maxRetries: 0, ...(row.hook ? { onPayload: (payload) => payload } : {}) };
    result = await module.stream(model, normalizeContext(row.context), options).result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  results.push(captured === null ? { none: result.errorMessage ?? result.stopReason } : captured);
}
process.stdout.write(JSON.stringify(results));
