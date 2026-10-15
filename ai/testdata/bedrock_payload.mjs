// Drive the installed pi-ai bedrock-converse-stream stream/streamSimple and report the ConverseStream command input handed to onPayload, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const bedrock = await import(pathToFileURL(dist + "api/bedrock-converse-stream.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const model = { id: probe.id, name: probe.name, api: "bedrock-converse-stream", provider: "amazon-bedrock", baseUrl: probe.baseUrl, reasoning: probe.reasoning, input: probe.input, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: 16384, ...(probe.compat ? { compat: probe.compat } : {}), ...(probe.levels ? { thinkingLevelMap: probe.levels } : {}) };
  let captured = null;
  const toolChoice = probe.options.toolChoice;
  const options = { ...probe.options, ...(toolChoice && !["auto", "any", "none"].includes(toolChoice) ? { toolChoice: { type: "tool", name: toolChoice } } : {}), apiKey: "bearer-token", onPayload: (payload) => { captured = JSON.stringify(payload, (_key, value) => (value instanceof Uint8Array ? Buffer.from(value).toString("base64") : value)); throw new Error("payload captured"); } };
  const run = probe.simple ? bedrock.streamSimple : bedrock.stream;
  let result;
  try {
    result = await run(model, normalizeContext(probe.context), options).result();
  } catch (error) { result = { stopReason: "thrown", errorMessage: String(error) }; }
  results.push(captured === null ? { none: result.errorMessage ?? result.stopReason } : captured);
}
process.stdout.write(JSON.stringify(results));
