// Drive the installed pi-ai openai-responses stream/streamSimple and report the request payload the SDK is handed, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const responses = await import(pathToFileURL(dist + "api/openai-responses.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const model = { id: "gpt-test", name: "gpt-test", api: "openai-responses", provider: probe.provider, baseUrl: probe.baseUrl, reasoning: probe.reasoning, input: probe.input, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 400000, maxTokens: 16384, ...(probe.compat ? { compat: probe.compat } : {}), ...(probe.levels ? { thinkingLevelMap: probe.levels } : {}) };
  let captured = null;
  const options = { ...probe.options, apiKey: probe.apiKey, onPayload: (payload) => { captured = JSON.stringify(payload); throw new Error("payload captured"); } };
  const run = probe.simple ? responses.streamSimple : responses.stream;
  let result;
  try {
    const stream = run(model, normalizeContext(probe.context), options);
    result = await stream.result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  results.push(captured === null ? { none: result.errorMessage ?? result.stopReason } : captured);
}
process.stdout.write(JSON.stringify(results));
