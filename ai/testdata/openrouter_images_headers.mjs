// Drive the pinned pi-ai openrouter-images generateImages and report the headers of the request it sends (through options.fetch).
// argv: version. stdin: [{provider, modelHeaders, identity, headers}] (a null header removes). stdout: [{name: value}] lower-cased header names.
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
  const model = { id: "m", name: "m", api: "openrouter-images", provider: probe.provider ?? "openrouter", baseUrl: "http://127.0.0.1:9/v1", input: ["text"], output: ["image"], cost: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 3 }, ...(probe.modelHeaders ? { headers: probe.modelHeaders } : {}) };
  let seen = {};
  const fetchFn = async (url, init) => {
    for (const [key, value] of new Headers(init?.headers ?? {})) seen[key.toLowerCase()] = value;
    return new Response('{"id":"x","choices":[{"message":{"content":"t"}}]}', { status: 200, headers: { "content-type": "application/json" } });
  };
  await generateImages(model, { input: [{ type: "text", text: "draw" }] }, { apiKey: "k", fetch: fetchFn, maxRetries: 0, ...(probe.identity ? { identity: probe.identity } : {}), ...(probe.headers ? { headers: probe.headers } : {}) });
  results.push(seen);
}
process.stdout.write(JSON.stringify(results));
