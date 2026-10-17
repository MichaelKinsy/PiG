// Drive the pinned pi-ai openrouter-images generateImages over scripted chat completion bodies (through options.fetch).
// argv: version. stdin: [{status, body}]. stdout: [{output, responseId, usage, stopReason, errorMessage}].
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { generateImages } = await import(pathToFileURL(dist + "api/openrouter-images.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const model = { id: "m", name: "m", api: "openrouter-images", provider: "openrouter", baseUrl: "http://127.0.0.1:9/v1", input: ["text"], output: probe.text ? ["image", "text"] : ["image"], cost: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 3 } };
  const fetchFn = async () => new Response(probe.body, { status: probe.status || 200, headers: { "content-type": "application/json" } });
  const out = await generateImages(model, { input: [{ type: "text", text: "draw" }] }, { apiKey: "k", fetch: fetchFn, maxRetries: 0 });
  const { timestamp, ...rest } = out;
  results.push(rest);
}
process.stdout.write(JSON.stringify(results));
