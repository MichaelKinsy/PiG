// Drive the installed pi-ai stream of each row's API kind over a reply the row gives, and report the message the stream ends with as
// JSON.stringify writes it (what Pi's session line holds), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
const modules = {};
let reply = "";
// Each SDK's default fetch is the global one at the time of the call: every request is answered with the row's event stream.
globalThis.fetch = async () => new Response(reply, { status: 200, headers: { "content-type": "text/event-stream" } });
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const row of JSON.parse(input)) {
  reply = row.reply;
  let result;
  try {
    const module = (modules[row.api] ??= await import(pathToFileURL(dist + "api/" + row.api + ".js")));
    const model = { id: row.id, name: row.id, api: row.api, provider: row.provider, baseUrl: row.baseUrl, reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: row.contextWindow, maxTokens: 16384 };
    result = await module.stream(model, normalizeContext(row.context), { apiKey: row.apiKey, maxRetries: 0 }).result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  results.push(JSON.stringify(result));
}
process.stdout.write(JSON.stringify(results));
