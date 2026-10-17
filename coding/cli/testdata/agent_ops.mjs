// Drive the installed Pi's Agent class over scripted operations (prompt, continue, steer, followUp, queue modes, clears, reset) and report each step's
// events, error and transcript, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const modules = root + "node_modules/@earendil-works/";
const { Agent } = await import(pathToFileURL(modules + "pi-agent-core/dist/index.js"));
const { toJsonEvent } = await import(pathToFileURL(root + "dist/modes/json-event.js"));
const { createAssistantMessageEventStream } = await import(pathToFileURL(modules + "pi-ai/dist/utils/event-stream.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const assistant = (content, stopReason, extra = {}) => ({ role: "assistant", content, api: "test", provider: "test", model: "mock", usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason, timestamp: 1, ...extra });
const user = (value) => ({ role: "user", content: [{ type: "text", text: value }], timestamp: 1 });
const results = [];
for (const probe of JSON.parse(input)) {
  let turn = 0;
  const streamFn = async () => {
    const script = (probe.turns ?? [])[turn++] ?? { text: "done", stop: "stop" };
    if (script.stop === "throw") throw new Error("stream failed");
    const message = assistant(script.text ? [{ type: "text", text: script.text }] : [], script.stop, script.stop === "error" || script.stop === "aborted" ? { errorMessage: script.stop } : {});
    const stream = createAssistantMessageEventStream();
    stream.push({ type: "start", partial: assistant([], "pending") });
    if (script.stop === "error" || script.stop === "aborted") stream.push({ type: "error", reason: script.stop, error: message });
    else stream.push({ type: "done", reason: script.stop, message });
    return stream;
  };
  const model = { id: "mock", name: "mock", api: "test", provider: "test", baseUrl: "", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 8192, maxTokens: 1024 };
  const agent = new Agent({ initialState: { model, tools: [], systemPrompt: "" }, streamFn, convertToLlm: (messages) => messages.filter((m) => ["user", "assistant", "toolResult", "system"].includes(m.role)) });
  let events = [];
  agent.subscribe(async (event) => { events.push(toJsonEvent(event)); });
  const steps = [];
  for (const op of probe.ops) {
    let error = null;
    events = [];
    try {
      switch (op.op) {
        case "prompt": await agent.prompt(op.text); break;
        case "continue": await agent.continue(); break;
        case "steer": agent.steer(user(op.text)); break;
        case "followUp": agent.followUp(user(op.text)); break;
        case "steerMode": agent.steeringMode = op.mode; break;
        case "followMode": agent.followUpMode = op.mode; break;
        case "clearSteer": agent.clearSteeringQueue(); break;
        case "clearFollow": agent.clearFollowUpQueue(); break;
        case "clearAll": agent.clearAllQueues(); break;
        case "reset": agent.reset(); break;
      }
    } catch (e) { error = e.message; }
    steps.push({ error, events, messages: JSON.parse(JSON.stringify(agent.state.messages)), queued: agent.hasQueuedMessages(), streaming: agent.state.isStreaming });
  }
  results.push(steps);
}
process.stdout.write(JSON.stringify(results));
