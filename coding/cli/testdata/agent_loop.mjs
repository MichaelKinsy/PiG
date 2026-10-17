// Drive the installed Pi's agentLoop over scripted assistant turns, tools, queues and hooks and report its events and returned messages, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const modules = root + "node_modules/@earendil-works/";
const { agentLoop } = await import(pathToFileURL(modules + "pi-agent-core/dist/index.js"));
const { Type } = await import(pathToFileURL(modules + "pi-ai/dist/index.js"));
const { toJsonEvent } = await import(pathToFileURL(root + "dist/modes/json-event.js"));
const { createAssistantMessageEventStream } = await import(pathToFileURL(modules + "pi-ai/dist/utils/event-stream.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const text = (value) => ({ content: [{ type: "text", text: value }], details: {} });
const behaviors = {
  echo: { schema: Type.Object({ text: Type.Optional(Type.String()) }), run: async (_id, args) => text(`echo:${args.text ?? ""}`) },
  slow: { schema: Type.Object({ text: Type.Optional(Type.String()) }), run: async (_id, args) => { await sleep(15); return text(`slow:${args.text ?? ""}`); } },
  fail: { schema: Type.Object({}), run: async () => { throw new Error("boom"); } },
  iserr: { schema: Type.Object({}), run: async () => ({ content: [{ type: "text", text: "bad" }], details: { code: 1 }, isError: true }) },
  term: { schema: Type.Object({}), run: async () => ({ content: [{ type: "text", text: "stop now" }], details: {}, terminate: true }) },
  upd: { schema: Type.Object({}), run: async (_id, _args, _signal, onUpdate) => { onUpdate?.({ content: [{ type: "text", text: "half" }], details: { step: 1 } }); onUpdate?.({ content: [{ type: "text", text: "most" }], details: { step: 2 } }); return text("upd done"); } },
  strict: { schema: Type.Object({ n: Type.Number() }), run: async (_id, args) => text(`n=${args.n}`) },
};
const assistant = (content, stopReason, extra = {}) => ({ role: "assistant", content, api: "test", provider: "test", model: "mock", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason, timestamp: 1, ...extra });
const results = [];
for (const probe of JSON.parse(input)) {
  const tools = probe.tools.map((name) => ({ name, label: name, description: name, parameters: behaviors[name].schema, executionMode: probe.toolModes?.[name], execute: behaviors[name].run }));
  let turn = 0;
  const streamFn = async () => {
    const script = (probe.turns ?? [])[turn++] ?? { text: "done", calls: [], stop: "stop" };
    const message = assistant([...(script.text ? [{ type: "text", text: script.text }] : []), ...(script.calls ?? []).map((call) => ({ type: "toolCall", id: call.id, name: call.name, arguments: call.args }))], script.stop, script.stop === "error" || script.stop === "aborted" ? { errorMessage: script.stop } : {});
    const stream = createAssistantMessageEventStream();
    stream.push({ type: "start", partial: assistant([], "pending") });
    if (probe.stream) {
      const done = [];
      const snapshot = (current) => assistant(structuredClone([...done, ...(current ? [current] : [])]), "pending");
      for (const block of message.content) {
        if (block.type === "text") {
          stream.push({ type: "text_start", contentIndex: done.length, partial: snapshot({ type: "text", text: "" }) });
          stream.push({ type: "text_delta", contentIndex: done.length, delta: block.text, partial: snapshot(block) });
          stream.push({ type: "text_end", contentIndex: done.length, content: block.text, partial: snapshot(block) });
        } else {
          stream.push({ type: "toolcall_start", contentIndex: done.length, partial: snapshot({ ...block, arguments: {} }) });
          stream.push({ type: "toolcall_delta", contentIndex: done.length, delta: JSON.stringify(block.arguments), partial: snapshot(block) });
          stream.push({ type: "toolcall_end", contentIndex: done.length, toolCall: structuredClone(block), partial: snapshot(block) });
        }
        done.push(block);
      }
    }
    if (script.stop === "error" || script.stop === "aborted") stream.push({ type: "error", reason: script.stop, error: message });
    else stream.push({ type: "done", reason: script.stop, message });
    return stream;
  };
  const steering = probe.steering.map((batch) => [...batch]);
  const followUp = probe.followUp.map((batch) => [...batch]);
  const user = (value) => ({ role: "user", content: [{ type: "text", text: value }], timestamp: 1 });
  const config = {
    model: { id: "mock", name: "mock", api: "test", provider: "test", baseUrl: "", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 8192, maxTokens: 1024 },
    convertToLlm: (messages) => messages.filter((m) => ["user", "assistant", "toolResult", "system"].includes(m.role)),
    toolExecution: probe.mode || undefined,
    getSteeringMessages: async () => (steering.shift() ?? []).map(user),
    getFollowUpMessages: async () => (followUp.shift() ?? []).map(user),
    beforeToolCall: async ({ toolCall }) => (probe.block?.[toolCall.name] ? { block: true, reason: probe.block[toolCall.name] } : undefined),
    afterToolCall: async ({ toolCall }) => {
      const override = probe.after?.[toolCall.name];
      if (!override) return undefined;
      return { ...(override.text ? { content: [{ type: "text", text: override.text }] } : {}), ...(override.isError !== null && override.isError !== undefined ? { isError: override.isError } : {}), ...(override.terminate !== null && override.terminate !== undefined ? { terminate: override.terminate } : {}) };
    },
  };
  const context = { systemPrompt: "", messages: probe.history.map((message, index) => (index % 2 === 0 ? user(message) : assistant([{ type: "text", text: message }], "stop"))), tools };
  const events = [];
  let messages = [];
  try {
    const stream = agentLoop([user(probe.prompt)], context, config, undefined, streamFn);
    for await (const event of stream) events.push(toJsonEvent(event));
    messages = await stream.result();
  } catch (error) { events.push({ type: "thrown", message: String(error) }); }
  results.push({ events, messages });
}
process.stdout.write(JSON.stringify(results));
