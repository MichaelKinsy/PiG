// Drive the installed pi-ai mistral-conversations stream/streamSimple and report the request payload the SDK is handed, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const mistral = await import(pathToFileURL(dist + "api/mistral-conversations.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const model = { id: "m", name: "m", api: "mistral-conversations", provider: "mistral", baseUrl: "http://127.0.0.1:9", reasoning: probe.reasoning, ...(probe.levels ? { thinkingLevelMap: probe.levels } : {}), input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 16384 };
  let captured = null;
  const options = { ...probe.options, apiKey: "fake-key", onPayload: (payload) => { captured = JSON.stringify(payload); return payload; } };
  const run = probe.simple ? mistral.streamSimple : mistral.stream;
  const stream = run(model, normalizeContext({ messages: [{ role: "user", content: "Hello", timestamp: 1 }] }), options);
  await stream.result();
  results.push(captured);
}
process.stdout.write(JSON.stringify(results));
