// Drive the installed pi-ai faux provider, never a translated oracle: which scripted-message members every event keeps.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const faux = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/providers/faux.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const KEYS = ["responseModel", "responseId", "providerThinkingLevel", "thinkingLevel", "diagnostics", "rawStopReason", "endTurn", "errorMessage", "stopReason"];
// A scripted durationMs is the sentinel 777; a value the stream measured is not reproducible, so it is reported as "measured".
const pick = (message) => ({
  ...Object.fromEntries(KEYS.filter((key) => message[key] !== undefined).map((key) => [key, message[key]])),
  ...(message.durationMs === undefined ? {} : { durationMs: message.durationMs === 777 ? 777 : "measured" }),
});
const results = [];
for (const probe of JSON.parse(input)) {
  const core = faux.createFauxCore({ api: "faux-probe", provider: "faux-probe", tokenSize: { min: 100, max: 100 } });
  const content = probe.content.map((block) => block.type === "text" ? faux.fauxText(block.text) : block.type === "thinking" ? faux.fauxThinking(block.thinking) : faux.fauxToolCall(block.name, block.arguments, { id: block.id }));
  core.setResponses([{ ...faux.fauxAssistantMessage(content, { stopReason: probe.stopReason, errorMessage: probe.errorMessage, timestamp: 1 }), ...probe.extra }]);
  const stream = core.stream(core.models[0], { messages: [] }, {});
  const events = [];
  for await (const event of stream) events.push({ type: event.type, message: pick(event.partial ?? event.message ?? event.error ?? {}) });
  results.push({ events, result: pick(await stream.result()) });
}
process.stdout.write(JSON.stringify(results));
