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
// In Pi the event.message an extension sees in message_end is the very object a later ctx.sessionManager.getEntries() returns in entry.message, and every handler of one message_end shares that message (each gets its own event object around it). Extensions rely on it: Paseo's Pi integration records the message_end user messages and then finds each one's session entry with `entry.message === message` to learn the entry id of the prompt it just sent. The Node runtime receives the event and the session log as separate wire payloads, so the two objects were equal in value and never the same object, and that lookup found nothing.
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

// One message_end, two handlers: Pi builds a new event object for each handler (runner.ts emitMessageEnd) and they share the message, so they see one message.
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

// The paths the identity relies on beyond the first test: the lazy read of the session file, a log-first scan that compares values, the clearing on a session switch, and the bound on the messages kept.
func TestNodeRuntimeEventMessageIdentityPaths(t *testing.T) {
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
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Runtime } from %q;

const user = (text = "hello", timestamp = 1000) => ({ role: "user", content: [{ type: "text", text }], timestamp });
const entry = (id, parentId, message) => ({ type: "message", id, parentId, timestamp: "2026-11-07T00:00:00.000Z", message });

function newRuntime(logRequested = true) {
  const runtime = new Runtime("/ext/identity-paths.mjs");
  runtime.conn = { respond() {}, call: async () => ({ entries: [], entryCount: 0, hasMore: false }), callSync: () => ({}), fireAndForget() {} };
  runtime.call = async () => ({ entries: [], entryCount: 1, hasMore: false });
  runtime.fireAndForget = () => {};
  runtime.sessionLogRequested = logRequested;
  return runtime;
}
let requestId = 0;
async function emit(runtime, message) {
  await runtime.handleRequest(++requestId, { method: "event", event: "message_end", handler_id: 1, args: { type: "message_end", message: structuredClone(message) } }, {});
}
const page = (runtime, session, entries) => runtime.applyState({ session: { sessionId: session, leafId: entries[entries.length - 1].id, entryCount: entries.length, entriesAppended: entries } });

// (a) The extension has not read the log when message_end arrives: the first getEntries reads the session file.
{
  const dir = mkdtempSync(join(tmpdir(), "pig-identity-"));
  const file = join(dir, "s.jsonl");
  writeFileSync(file, JSON.stringify({ type: "session", id: "s" }) + "\n" + JSON.stringify(entry("e0", null, user())) + "\n");
  const runtime = newRuntime(false);
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  runtime.applyState({ session: { sessionId: "s", sessionFile: file } });
  await emit(runtime, user());
  assert.equal(runtime.ctx.sessionManager.getEntries()[0].message, seen[0], "the file read shares the event's message");
}

// (b) Log first, with a newer lookalike: the scan compares values, so it finds the older equal entry and leaves the lookalike alone.
{
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  page(runtime, "s", [entry("e0", null, user()), entry("e1", "e0", user("different"))]);
  await emit(runtime, user());
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.equal(entries[0].message, seen[0]);
  assert.notEqual(entries[1].message, seen[0]);
}

// (c) A session switch drops the messages of the outgoing session: an equal message in the destination's log is not shared.
{
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  page(runtime, "s1", [entry("a0", null, user("first"))]);
  await emit(runtime, user());
  page(runtime, "s2", [entry("b0", null, user())]);
  assert.notEqual(runtime.ctx.sessionManager.getEntries()[0].message, seen[0]);
}

// (d) Only the newest 64 messages are kept: past that the oldest is no longer shared, and the ones inside the bound still are.
{
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  for (let i = 1; i <= 70; i++) await emit(runtime, user("m" + i, i));
  page(runtime, "s", [entry("e1", null, user("m1", 1)), entry("e10", "e1", user("m10", 10)), entry("e70", "e10", user("m70", 70))]);
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.notEqual(entries[0].message, seen[0], "m1 was evicted");
  assert.equal(entries[1].message, seen[9], "m10 is inside the bound");
  assert.equal(entries[2].message, seen[69], "m70 is the newest");
}
`, runtimeURL)
	if output, err := exec.Command(node, "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node message_end identity paths: %v\n%s", err, output)
	}
}
