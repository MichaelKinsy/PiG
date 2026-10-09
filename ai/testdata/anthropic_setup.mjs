// Drive the installed pi-ai anthropicMessagesApi over requests that fail during setup, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const { anthropicMessagesApi } = await import(pathToFileURL(dist + "api/anthropic-messages.lazy.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const model = { id: "m", name: "m", api: "anthropic-messages", provider: probe.provider, baseUrl: "http://127.0.0.1:1", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 1000, maxTokens: 100 };
  const controller = new AbortController();
  if (probe.aborted) controller.abort();
  const options = { ...probe.options, ...(probe.aborted ? { signal: controller.signal } : {}) };
  const events = [];
  const stream = anthropicMessagesApi()[probe.simple ? "streamSimple" : "stream"](model, normalizeContext({ messages: [{ role: "user", content: "hi", timestamp: 1 }] }), options);
  for await (const event of stream) events.push(event.type);
  const message = await stream.result();
  results.push({ events, stopReason: message.stopReason, errorMessage: message.errorMessage ?? null, api: message.api, provider: message.provider, model: message.model, content: message.content, usage: message.usage });
}
process.stdout.write(JSON.stringify(results));
