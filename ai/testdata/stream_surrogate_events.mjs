// Drive the installed pi-ai stream of each row's API kind over a reply the row gives, and report the text, thinking and tool-argument deltas of the events
// the stream yields, each as JSON.stringify writes it (what an RPC client reads), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
const modules = {};
let reply = "";
globalThis.fetch = async () => new Response(reply, { status: 200, headers: { "content-type": "text/event-stream" } });
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const row of JSON.parse(input)) {
  reply = row.reply;
  const deltas = [];
  try {
    const module = (modules[row.api] ??= await import(pathToFileURL(dist + "api/" + row.api + ".js")));
    const model = { id: row.id, name: row.id, api: row.api, provider: row.provider, baseUrl: row.baseUrl, reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: row.contextWindow, maxTokens: 16384 };
    for await (const event of module.stream(model, normalizeContext(row.context), { apiKey: row.apiKey, maxRetries: 0 })) {
      if (event.type === "text_delta" || event.type === "thinking_delta" || event.type === "toolcall_delta") deltas.push(event.type + " " + JSON.stringify(event.delta));
    }
  } catch (error) { deltas.push("thrown " + String(error)); }
  results.push(deltas.join("\n"));
}
process.stdout.write(JSON.stringify(results));
