// Drive the installed pi-ai pi-messages stream over a random transcript and report the request payload the hook is handed, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const piMessages = await import(pathToFileURL(dist + "api/pi-messages.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const model = { id: probe.id, name: probe.id, api: "pi-messages", provider: "radius", baseUrl: "http://127.0.0.1:9", reasoning: probe.reasoning, input: probe.input, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 16384 };
  let captured = null;
  const options = { ...probe.options, apiKey: "fake-key", onPayload: (payload) => { captured = JSON.stringify(payload); throw new Error("payload captured"); } };
  let result;
  try {
    const stream = piMessages.stream(model, normalizeContext(probe.context), options);
    result = await stream.result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  results.push(captured === null ? { none: result.errorMessage ?? result.stopReason } : captured);
}
process.stdout.write(JSON.stringify(results));
