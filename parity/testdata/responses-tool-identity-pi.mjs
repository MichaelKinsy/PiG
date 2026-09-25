import { homedir } from "node:os";
import { pathToFileURL } from "node:url";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

const version = readFileSync("coding/pigversion/pigversion.go", "utf8").match(/UpstreamVersion = "([^"]+)"/)[1];
const install = join(homedir(), ".local/share/mise/installs/npm-earendil-works-pi-coding-agent", version);
const root = process.env.PI_PACKAGE_ROOT ?? [
  join(install, "node_modules/@earendil-works/pi-coding-agent"),
  join(install, "lib/node_modules/@earendil-works/pi-coding-agent"),
].find((path) => existsSync(join(path, "package.json")));
if (!root) throw new Error("Pinned Pi package not found; set PI_PACKAGE_ROOT");
const installedVersion = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version;
if (installedVersion !== version) throw new Error(`Pi version ${installedVersion}, expected ${version}`);
const aiRoot = join(root, "node_modules/@earendil-works/pi-ai/dist");
const { processResponsesStream } = await import(pathToFileURL(join(aiRoot, "api/openai-responses-shared.js")));
const { AssistantMessageEventStream } = await import(pathToFileURL(join(aiRoot, "utils/event-stream.js")));

for (const [name, fields] of [
  ["repeated", { id: "fc_original", call_id: "call_original", name: "read" }],
  ["omitted", {}],
  ["different", { id: "fc_final", call_id: "call_final", name: "other" }],
]) {
  const cost = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 };
  const output = { role: "assistant", content: [], api: "openai-responses", provider: "openai", model: "probe", usage: { ...cost, totalTokens: 0, cost }, stopReason: "stop", timestamp: 0 };
  const stream = new AssistantMessageEventStream();
  const print = (stage, call) => console.log(`${name}:${stage} ${call.id} ${call.name}`);
  // Observe each snapshot at emission; Pi mutates its partial object in place.
  const push = stream.push.bind(stream);
  stream.push = (event) => {
    if (event.type.startsWith("toolcall_")) print(event.type.slice("toolcall_".length), event.toolCall ?? event.partial.content[event.contentIndex]);
    push(event);
  };
  const events = [
    { type: "response.output_item.added", output_index: 0, item: { type: "function_call", id: "fc_original", call_id: "call_original", name: "read", arguments: "" } },
    { type: "response.function_call_arguments.delta", output_index: 0, delta: '{"path":"main.go"}' },
    { type: "response.output_item.done", output_index: 0, item: { type: "function_call", ...fields, namespace: "functions", arguments: '{"path":"final.go"}' } },
    { type: "response.completed", response: { status: "completed" } },
  ];
  await processResponsesStream((async function* () { yield* events; })(), output, stream, { id: "probe", cost });
  const call = output.content[0];
  print("result", call);
  console.log(`${name}:final ${call.namespace} ${call.arguments.path}`);
}
