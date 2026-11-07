package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// upstream: packages/coding-agent/src/core/agent-session.ts _handleAgentEvent (the extensions get message_end first, then sessionManager.appendMessage(event.message)) and session-manager.ts appendMessage (the entry holds the message object it was given).
//
// In Pi the event.message an extension sees in message_end is the very object a later ctx.sessionManager.getEntries() returns in entry.message, and every handler of one event shares it. Extensions rely on it: Paseo's Pi integration records the message_end user messages and then finds each one's session entry with `entry.message === message` to learn the entry id of the prompt it just sent. The Node runtime receives the event and the session log as separate wire payloads, so the two objects were equal in value and never the same object, and that lookup found nothing.
func TestNodeRuntimeEventMessageIsTheSessionEntryMessage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node not found: %v", err)
	}
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	runtimeURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(runtimePath)}).String()
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;

const user = () => ({ role: "user", content: [{ type: "text", text: "hello" }], timestamp: 1000 });
const assistant = () => ({ role: "assistant", content: [{ type: "text", text: "hi" }], api: "x", provider: "p", model: "m", usage: { input: 1, output: 2 }, stopReason: "stop", timestamp: 2000 });
const toolResult = () => ({ role: "toolResult", toolCallId: "call_1", toolName: "bash", content: [{ type: "text", text: "ok" }], isError: false, timestamp: 3000 });
const entry = (id, parentId, message) => ({ type: "message", id, parentId, timestamp: "2026-11-07T00:00:00.000Z", message });

function newRuntime() {
  const runtime = new Runtime("/ext/identity.mjs");
  runtime.conn = { respond() {}, call: async () => ({}), callSync: () => ({}), fireAndForget() {} };
  runtime.fireAndForget = () => {};
  runtime.sessionLogRequested = true;
  return runtime;
}

// push applies one host page, as the host's state push carries it.
let hostLog = [];
function push(runtime, entries) {
  hostLog = [...hostLog, ...entries];
  runtime.applyState({ session: { sessionId: "s", leafId: entries[entries.length - 1].id, entryCount: hostLog.length, entriesAppended: entries } });
}

// emit delivers one message_end the way the host does: a fresh decoded payload for each handler.
let requestId = 0;
async function emit(runtime, handlerId, message) {
  await runtime.handleRequest(++requestId, { method: "event", event: "message_end", handler_id: handlerId, args: { type: "message_end", message: structuredClone(message) } }, {});
}

// Pi's order: the extensions see message_end, then the entry is appended and the log reaches the extension.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  for (const [i, make] of [user, assistant, toolResult].entries()) {
    await emit(runtime, 1, make());
    push(runtime, [entry("e" + i, i === 0 ? null : "e" + (i - 1), make())]);
  }
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.equal(entries.length, 3);
  for (let i = 0; i < 3; i++) {
    assert.equal(entries[i].message, seen[i], "entry " + i + " holds the event's message object");
    assert.equal(runtime.ctx.sessionManager.getEntry("e" + i).message, seen[i], "getEntry " + i);
  }
  assert.equal(runtime.ctx.sessionManager.getBranch()[2].message, seen[2], "getBranch holds the same objects");
  // The entries Paseo reads at the next message_start are the same objects again.
  assert.equal(runtime.ctx.sessionManager.getEntries()[0].message, seen[0], "stable across reads");
}

// The log can reach the extension before its handler runs; the object is shared in that order too.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  push(runtime, [entry("e0", null, user())]);
  await emit(runtime, 1, user());
  assert.equal(runtime.ctx.sessionManager.getEntries()[0].message, seen[0], "log first, event second");
}

// One event, two handlers: Pi passes both the same event object, so they see one message.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  runtime.on("message_end", (event) => { seen.push(event.message); });
  await emit(runtime, 1, user());
  await emit(runtime, 2, user());
  assert.equal(seen[0], seen[1], "both handlers of one message_end share the message");
}

// Messages that merely look alike are not conflated: same role and timestamp, different content stays two objects, and an entry no event carried keeps its own message.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  const other = { ...user(), content: [{ type: "text", text: "different" }] };
  await emit(runtime, 1, user());
  push(runtime, [entry("e0", null, other)]);
  const held = runtime.ctx.sessionManager.getEntries()[0].message;
  assert.notEqual(held, seen[0]);
  assert.deepEqual(held, other);
}
`, runtimeURL)
	if output, err := exec.Command(node, "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node message_end identity: %v\n%s", err, output)
	}
}
