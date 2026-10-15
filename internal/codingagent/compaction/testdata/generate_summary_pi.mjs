// Run the installed Pi's generateSummary and generateSummaryWithUsage (core/compaction/compaction.ts) with a recording streamFn and report the request it was given: the system prompt, the user prompt, and the options (token budget, auth, headers, environment, reasoning, session id, cache retention), plus the text and usage the call returns.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { generateSummary, generateSummaryWithUsage } = await import(pathToFileURL(root + "dist/core/compaction/compaction.js"));
const ai = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-ai/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const scenario of JSON.parse(input)) {
  const model = { id: "m", name: "M", api: "anthropic-messages", provider: "anthropic", baseUrl: "https://example.test", reasoning: scenario.reasoning, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 200000, maxTokens: scenario.maxTokens, ...(scenario.reasoning ? { thinkingLevelMap: undefined } : {}) };
  let seen = null;
  const streamFn = (m, context, options) => {
    const { signal, ...rest } = options;
    seen = { system: context.messages.filter((x) => x.role === "system").map((x) => x.content), roles: context.messages.map((x) => x.role), user: context.messages.filter((x) => x.role === "user").flatMap((x) => x.content.map((c) => c.text)), options: rest };
    const stream = ai.createAssistantMessageEventStream();
    const message = { role: "assistant", content: [{ type: "text", text: "summary text" }], api: m.api, provider: m.provider, model: m.id, usage: { input: 7, output: 3, cacheRead: 1, cacheWrite: 2, totalTokens: 13, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "stop", timestamp: 1 };
    stream.push({ type: "done", reason: "stop", message });
    stream.end(message);
    return stream;
  };
  const args = [scenario.messages, model, scenario.reserve, scenario.apiKey ?? undefined, scenario.headers ?? undefined, undefined, scenario.custom || undefined, scenario.previous || undefined, scenario.thinking || undefined, streamFn, scenario.env ?? undefined, undefined, undefined, scenario.sessionId || undefined];
  let out;
  try {
    if (scenario.withUsage) {
      const r = await generateSummaryWithUsage(...args);
      out = { text: r.text, usage: { input: r.usage.input, output: r.usage.output, cacheRead: r.usage.cacheRead, cacheWrite: r.usage.cacheWrite, totalTokens: r.usage.totalTokens } };
    } else {
      out = { text: await generateSummary(...args) };
    }
  } catch (error) {
    out = { thrown: String(error && error.message) };
  }
  out.seen = seen;
  results.push(out);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
