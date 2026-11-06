// Run the installed Pi's AgentSession._emitExtensionEvent (agent-session.ts) over a recording extension runner, never a translated oracle.
// The real method and the real _replaceMessageInPlace run on an object made from AgentSession.prototype; the runner records every event it is asked to emit (as the JSON
// a handler would stringify), answers message_end with the probe's replacement, and the turn_end boundary dispatch records the turn index it runs under. Date.now is
// replaced so the turn_start timestamp is checkable.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const load = (path) => import(pathToFileURL(root + path));
const { AgentSession } = await load("dist/core/agent-session.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
let clock = 1_700_000_000_000;
Date.now = () => ++clock;
const results = [];
for (const probe of JSON.parse(input)) {
  const messages = structuredClone(probe.messages);
  let record = [];
  let replacement;
  const stub = Object.create(AgentSession.prototype);
  stub._turnIndex = 0;
  stub._boundaryDispatchedMessages = new WeakSet();
  stub._extensionRunner = {
    emit: async (event) => { record.push({ kind: "emit", event: JSON.stringify(event) }); },
    emitMessageEnd: async (event) => { record.push({ kind: "emitMessageEnd", event: JSON.stringify(event) }); return replacement; },
  };
  stub._dispatchTurnEndBoundary = async function (message, toolResults) {
    record.push({ kind: "boundary", turnIndex: this._turnIndex, message: JSON.stringify(message), toolResults: JSON.stringify(toolResults) });
    return false;
  };
  const steps = [];
  for (const step of probe.steps) {
    record = [];
    const ref = (name) => messages[name];
    replacement = step.replaceWithSelf ? ref(step.message) : step.replacement === undefined ? undefined : structuredClone(step.replacement);
    let event;
    switch (step.type) {
      case "agent_start": event = { type: "agent_start" }; break;
      case "agent_end": event = { type: "agent_end", messages: step.messages.map(ref) }; break;
      case "turn_start": event = { type: "turn_start", turnIndex: step.turnIndex }; break;
      case "turn_end":
        event = { type: "turn_end", message: ref(step.message), toolResults: step.toolResults.map(ref) };
        if (step.preDispatched) stub._boundaryDispatchedMessages.add(event.message);
        break;
      case "message_start": event = { type: "message_start", message: ref(step.message) }; break;
      case "message_update": event = { type: "message_update", message: ref(step.message), assistantMessageEvent: step.assistantMessageEvent }; break;
      case "message_end": event = { type: "message_end", message: ref(step.message) }; break;
      case "tool_execution_start": event = { type: "tool_execution_start", toolCallId: step.toolCallId, toolName: step.toolName, args: step.args }; break;
      case "tool_execution_update": event = { type: "tool_execution_update", toolCallId: step.toolCallId, toolName: step.toolName, args: step.args, partialResult: step.partialResult }; break;
      case "tool_execution_end": {
        event = { type: "tool_execution_end", toolCallId: step.toolCallId, toolName: step.toolName, result: step.result, isError: step.isError };
        if (step.durationMs !== undefined) event.durationMs = step.durationMs;
        break;
      }
      default: throw new Error("unknown step " + step.type);
    }
    const before = clock;
    await stub._emitExtensionEvent(event);
    steps.push({ record, message: event.message === undefined ? null : JSON.stringify(event.message), turnIndex: stub._turnIndex, clockAdvanced: clock - before });
  }
  results.push(steps);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
