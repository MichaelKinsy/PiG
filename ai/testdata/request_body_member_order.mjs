// Drive the installed pi-ai anthropic-messages and openai-codex-responses streams against the Go test server, which records the bytes Pi itself sends: never a translated oracle.
// stdin: {anthropic: [{baseUrl, replacement}], codex: {baseUrl, nullBaseUrl, apiKey, sessionId}}. Each Anthropic probe's onPayload returns JSON.parse(replacement); the Codex
// onPayload adds members around the payload Pi built (hook below) and runs two turns over one cached WebSocket, so the second request is a continuation.
// stdout: {anthropic: [stopReason or errorMessage], codex: [stopReason or errorMessage]}.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const dist = root + "node_modules/@earendil-works/pi-ai/dist/";
const anthropic = await import(pathToFileURL(dist + "api/anthropic-messages.js"));
const codex = await import(pathToFileURL(dist + "api/openai-codex-responses.js"));
const { normalizeContext } = await import(pathToFileURL(dist + "utils/transcript.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const job = JSON.parse(input);
const cost = { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 };
const outcome = (result) => (result.stopReason === "error" ? result.errorMessage : result.stopReason);
const results = { anthropic: [], codex: [] };
for (const probe of job.anthropic) {
  const model = { id: "claude-fable-5-1", name: "claude-fable-5-1", api: "anthropic-messages", provider: "anthropic", baseUrl: probe.baseUrl, reasoning: false, input: ["text"], cost, contextWindow: 200000, maxTokens: 16384 };
  const stream = anthropic.stream(model, normalizeContext({ messages: [{ role: "user", content: "Hello", timestamp: 1 }] }), { apiKey: "test-key", onPayload: () => JSON.parse(probe.replacement) });
  results.anthropic.push(outcome(await stream.result()));
}
// The same text edit the Go side makes: a body type, a nested object and an array-index key around the members Pi built.
const hook = (payload) => JSON.parse('{"type":"hooked","zeta":{"y":1,"x":2},' + JSON.stringify(payload).slice(1, -1) + ',"7":"seven"}');
const model = { id: "gpt-5.2", name: "gpt-5.2", api: "openai-codex-responses", provider: "openai-codex", baseUrl: job.codex.baseUrl, reasoning: false, input: ["text"], cost, contextWindow: 400000, maxTokens: 16384 };
const options = { apiKey: job.codex.apiKey, sessionId: job.codex.sessionId, transport: "websocket-cached", onPayload: hook };
const first = await codex.stream(model, normalizeContext({ messages: [{ role: "user", content: "one", timestamp: 1 }] }), options).result();
results.codex.push(outcome(first));
const second = await codex.stream(model, normalizeContext({ messages: [{ role: "user", content: "one", timestamp: 1 }, first, { role: "user", content: "two", timestamp: 2 }] }), options).result();
results.codex.push(outcome(second));
// A hook that returns null leaves {type: "response.create", ...null}; without a session no debug stats read the body first.
const nullModel = { ...model, baseUrl: job.codex.nullBaseUrl };
results.codex.push(outcome(await codex.stream(nullModel, normalizeContext({ messages: [{ role: "user", content: "one", timestamp: 1 }] }), { apiKey: job.codex.apiKey, transport: "websocket", onPayload: () => null }).result()));
process.stdout.write(JSON.stringify(results));
process.exit(0);
