package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// nodeIdentityScript runs body, an ES module fragment, over the helpers the identity tests share, with node.
func nodeIdentityScript(t *testing.T, name, body string) {
	t.Helper()
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
const assistant = (text = "hi") => ({ role: "assistant", content: [{ type: "text", text }], api: "x", provider: "p", model: "m", usage: { input: 1, output: 2 }, stopReason: "stop", timestamp: 2000 });
const toolResult = () => ({ role: "toolResult", toolCallId: "call_1", toolName: "bash", content: [{ type: "text", text: "ok" }], isError: false, timestamp: 3000 });
const entry = (id, parentId, message) => ({ type: "message", id, parentId, timestamp: "2026-11-07T00:00:00.000Z", message });

// One process, one table: the sequences of every block are distinct, as the host's per-session counter makes them.
let lastSeq = 0;
const nextSeq = () => ++lastSeq;

let extensions = 0;
function newRuntime(logRequested = true) {
  const runtime = new Runtime("/ext/identity-" + ++extensions + ".mjs");
  runtime.conn = { respond() {}, call: async () => ({ entries: [], entryCount: 0, hasMore: false }), callSync: () => ({}), fireAndForget() {} };
  runtime.call = async () => ({ entries: [], entryCount: 1, hasMore: false });
  runtime.fireAndForget = () => {};
  runtime.sessionLogRequested = logRequested;
  return runtime;
}

// emit delivers one event the way the host does: a freshly decoded payload for each handler, with the sequences of the messages it carries.
let requestId = 0;
async function emit(runtime, event, handlerId, payload, seqs) {
  await runtime.handleRequest(++requestId, { method: "event", event, handler_id: handlerId, args: { type: event, ...structuredClone(payload) }, ...(seqs ? { message_seqs: seqs } : {}) }, {});
}
const messageEnd = (runtime, message, seq, handlerId = 1) => emit(runtime, "message_end", handlerId, { message }, seq === undefined ? undefined : [seq]);

// push applies one host state push: a page of the log, and the message entries it names.
let hostLog = [];
function push(runtime, entries, pairs = [], session = "s") {
  hostLog = [...hostLog, ...entries];
  runtime.applyState({ session: { sessionId: session, leafId: entries.length ? entries[entries.length - 1].id : undefined, entryCount: hostLog.length, entriesAppended: entries, messageEntries: pairs } });
}

%s
`, runtimeURL, body)
	if output, err := exec.Command(node, "--expose-gc", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, output)
	}
}

// upstream: packages/coding-agent/src/core/agent-session.ts _handleAgentEvent (the extensions get message_end first, then sessionManager.appendMessage(event.message)) and session-manager.ts appendMessage (the entry holds the message object it was given).
//
// In Pi the event.message an extension sees in message_end is the very object a later ctx.sessionManager.getEntries() returns in entry.message. Extensions rely on it: Paseo's Pi integration records the message_end user messages and then finds each one's session entry with `entry.message === message` to learn the entry id of the prompt it just sent. The Node runtime receives the event and the session log as separate wire payloads, so the host numbers each message (message_seqs) and names the entry that persisted it (messageEntries), and the runtime makes the two one object.
func TestNodeRuntimeEventMessageIsTheSessionEntryMessage(t *testing.T) {
	nodeIdentityScript(t, "node message_end identity", `
// Pi's order: the extensions see message_end, then the entry is appended and the log reaches the extension with its pair.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  for (const [i, make] of [user, assistant, toolResult].entries()) {
    const seq = nextSeq();
    await messageEnd(runtime, make(), seq);
    push(runtime, [entry("e" + i, i === 0 ? null : "e" + (i - 1), make())], [{ id: "e" + i, seq }]);
  }
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.equal(entries.length, 3);
  for (let i = 0; i < 3; i++) {
    assert.equal(entries[i].message, seen[i], "entry " + i + " holds the event's message object");
    assert.equal(runtime.ctx.sessionManager.getEntry("e" + i).message, seen[i], "getEntry " + i);
  }
  assert.equal(runtime.ctx.sessionManager.getBranch()[2].message, seen[2], "getBranch holds the same objects");
  assert.equal(runtime.ctx.sessionManager.getEntries()[0].message, seen[0], "stable across reads");
}

// The pair can reach the extension after the page that holds its entry, or before it.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  const first = nextSeq(), second = nextSeq();
  await messageEnd(runtime, user("a"), first);
  await messageEnd(runtime, user("b"), second);
  push(runtime, [entry("e0", null, user("a"))]);
  push(runtime, [], [{ id: "e0", seq: first }]);
  push(runtime, [], [{ id: "e1", seq: second }]);
  push(runtime, [entry("e1", "e0", user("b"))]);
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.equal(entries[0].message, seen[0], "page first, pair second");
  assert.equal(entries[1].message, seen[1], "pair first, page second");
}

// One message_end, two handlers: Pi builds a new event object for each handler (runner.ts emitMessageEnd) and they share the message, so they see one message.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  runtime.on("message_end", (event) => { seen.push(event.message); });
  const seq = nextSeq();
  await messageEnd(runtime, user(), seq, 1);
  await messageEnd(runtime, user(), seq, 2);
  assert.equal(seen[0], seen[1], "both handlers of one message_end share the message");
}

// Messages that merely look alike are not conflated: an entry the host did not name keeps its own message, however equal it is, and a pair naming another message binds that one only.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  const seq = nextSeq();
  await messageEnd(runtime, user(), seq);
  push(runtime, [entry("e0", null, user()), entry("e1", "e0", user())], [{ id: "e1", seq }]);
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.notEqual(entries[0].message, seen[0], "an equal entry of another message is not the event's");
  assert.equal(entries[1].message, seen[0]);
}
`)
}

// A message_end handler that returns a replacement. Pi writes the replacement into the original message object in place and stores that object (agent-session.ts _replaceMessageInPlace), so every handler that ran before the replacement holds the entry's object, now with the replacement's value; a handler after it holds the replacement, which is no object the entry has.
func TestNodeRuntimeEventMessageIdentityAcrossAReplacement(t *testing.T) {
	nodeIdentityScript(t, "node message_end replacement identity", `
const replaced = (message) => ({ ...message, content: [...message.content, { type: "text", text: "(edited)" }] });

// One extension, two handlers: the first holds the message the entry stores, the second holds the replacement the first returned.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [[], []];
  runtime.on("message_end", (event) => { seen[0].push(event.message); return { message: replaced(event.message) }; });
  runtime.on("message_end", (event) => { seen[1].push(event.message); });
  const seq = nextSeq();
  await messageEnd(runtime, user(), seq, 1);
  // The host sends the second handler the replacement, and no number: it is no object Pi shares.
  await messageEnd(runtime, replaced(user()), undefined, 2);
  push(runtime, [entry("e0", null, replaced(user()))], [{ id: "e0", seq }]);
  const stored = runtime.ctx.sessionManager.getEntries()[0].message;
  assert.equal(stored, seen[0][0], "the handler before the replacement holds the entry's object");
  assert.deepEqual(stored, replaced(user()), "the object holds the stored, replaced message");
  assert.notEqual(stored, seen[1][0], "the handler after the replacement holds the replacement");
  assert.deepEqual(seen[1][0], replaced(user()));
}

// Two extensions: Paseo's records the message and finds its entry; the second extension rewrites user messages. Paseo's runs first, so it holds the object the entry stores.
{
  hostLog = [];
  const paseo = newRuntime();
  const recorded = [];
  paseo.on("message_end", (event) => { recorded.push(event.message); });
  const seq = nextSeq();
  await messageEnd(paseo, user(), seq);
  push(paseo, [entry("e0", null, replaced(user()))], [{ id: "e0", seq }]);
  const entries = paseo.ctx.sessionManager.getEntries();
  assert.equal(entries.findIndex((candidate) => candidate.message === recorded[0]), 0, "entry.message === message although another extension edited it");
  assert.deepEqual(recorded[0], replaced(user()));
}

// The extension that edits a message in a cell with a recorder: both runtimes of one process hold the one object, and the entry of either shows the replacement.
{
  hostLog = [];
  const recorder = newRuntime();
  const editor = newRuntime();
  const recorded = [];
  recorder.on("message_end", (event) => { recorded.push(event.message); });
  let edited;
  editor.on("message_end", (event) => { edited = event.message; return { message: replaced(event.message) }; });
  const seq = nextSeq();
  await messageEnd(recorder, user(), seq);
  await messageEnd(editor, user(), seq);
  assert.equal(edited, recorded[0], "extensions of one cell hold one object for one message");
  push(recorder, [entry("e0", null, replaced(user()))], [{ id: "e0", seq }]);
  push(editor, [], [{ id: "e0", seq }]);
  assert.equal(recorder.ctx.sessionManager.getEntries()[0].message, recorded[0]);
  assert.deepEqual(recorded[0], replaced(user()));
}
`)
}

// Pi hands the one object to message_start (system, user and toolResult messages), turn_end and agent_end too; an assistant message_start carries the partial message, which is another object.
func TestNodeRuntimeEventMessageIdentityInOtherEvents(t *testing.T) {
	nodeIdentityScript(t, "node message identity across events", `
{
  const runtime = newRuntime();
  const starts = [], ends = [], turns = [], agents = [];
  runtime.on("message_start", (event) => { starts.push(event.message); });
  runtime.on("message_end", (event) => { ends.push(event.message); });
  runtime.on("turn_end", (event) => { turns.push(event); });
  runtime.on("agent_end", (event) => { agents.push(event); });
  const userSeq = nextSeq(), assistantSeq = nextSeq(), resultSeq = nextSeq();
  await emit(runtime, "message_start", 1, { message: user() }, [userSeq]);
  await messageEnd(runtime, user(), userSeq, 2);
  assert.equal(starts[0], ends[0], "message_start and message_end share a user message");
  await emit(runtime, "message_start", 1, { message: assistant("") }, undefined);
  await messageEnd(runtime, assistant(), assistantSeq, 2);
  assert.notEqual(starts[1], ends[1], "an assistant message_start is the partial message");
  await emit(runtime, "message_start", 1, { message: toolResult() }, [resultSeq]);
  await messageEnd(runtime, toolResult(), resultSeq, 2);
  assert.equal(starts[2], ends[2], "message_start and message_end share a toolResult message");
  await emit(runtime, "turn_end", 3, { turnIndex: 0, message: assistant(), toolResults: [toolResult()] }, [assistantSeq, resultSeq]);
  assert.equal(turns[0].message, ends[1], "turn_end.message is the assistant message");
  assert.equal(turns[0].toolResults[0], ends[2], "turn_end.toolResults holds the tool result");
  await emit(runtime, "agent_end", 4, { messages: [user(), assistant(), toolResult()] }, [userSeq, assistantSeq, resultSeq]);
  assert.deepEqual(agents[0].messages.map((message, i) => message === ends[i]), [true, true, true], "agent_end.messages holds the same objects");
}

// A turn_end message the extension holds with an older value takes the value the host sent: it is one object with the stored message.
{
  const runtime = newRuntime();
  const ends = [], turns = [];
  runtime.on("message_end", (event) => { ends.push(event.message); });
  runtime.on("turn_end", (event) => { turns.push(event); });
  const seq = nextSeq();
  await messageEnd(runtime, assistant("before"), seq);
  await emit(runtime, "turn_end", 2, { turnIndex: 0, message: assistant("after"), toolResults: [] }, [seq]);
  assert.equal(turns[0].message, ends[0]);
  assert.equal(ends[0].content[0].text, "after");
}

// Two extensions of one cell hold one object for a message_start's message.
{
  const first = newRuntime(), second = newRuntime();
  const seen = [];
  first.on("message_start", (event) => { seen.push(event.message); });
  second.on("message_start", (event) => { seen.push(event.message); });
  const seq = nextSeq();
  await emit(first, "message_start", 1, { message: user() }, [seq]);
  await emit(second, "message_start", 1, { message: user() }, [seq]);
  assert.equal(seen[0], seen[1]);
}
`)
}

// The paths the identity relies on beyond the first test: the lazy read of the session file, a session switch, and the bound on the messages kept.
func TestNodeRuntimeEventMessageIdentityPaths(t *testing.T) {
	nodeIdentityScript(t, "node message_end identity paths", `
// (a) The extension has not read the log when message_end arrives: the first getEntries reads the session file, and the pair the host sent binds the entry found there.
{
  const dir = mkdtempSync(join(tmpdir(), "pig-identity-"));
  const file = join(dir, "s.jsonl");
  writeFileSync(file, JSON.stringify({ type: "session", id: "s" }) + "\n" + JSON.stringify(entry("e0", null, user())) + "\n");
  const runtime = newRuntime(false);
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  runtime.applyState({ session: { sessionId: "s", sessionFile: file } });
  const seq = nextSeq();
  await messageEnd(runtime, user(), seq);
  runtime.applyState({ session: { sessionId: "s", messageEntries: [{ id: "e0", seq }] } });
  assert.equal(runtime.ctx.sessionManager.getEntries()[0].message, seen[0], "the file read shares the event's message");
}

// (b) A session switch: an equal message in the destination's log is not the event's, and a pair of the outgoing session binds nothing there.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  const seq = nextSeq();
  push(runtime, [entry("a0", null, user("first"))], [], "s1");
  await messageEnd(runtime, user(), seq);
  hostLog = [];
  push(runtime, [entry("b0", null, user())], [], "s2");
  assert.notEqual(runtime.ctx.sessionManager.getEntries()[0].message, seen[0]);
}

// (c) Only the newest 128 messages are kept: past that the oldest is no longer shared, and the ones inside the bound still are.
{
  hostLog = [];
  const runtime = newRuntime();
  const seen = [], seqs = [];
  runtime.on("message_end", (event) => { seen.push(event.message); });
  // The runtime learns its session before it numbers a message (the process may hold another one's, which a switch drops).
  push(runtime, [], []);
  for (let i = 1; i <= 140; i++) { seqs.push(nextSeq()); await messageEnd(runtime, user("m" + i, i), seqs[i - 1]); }
  push(runtime, [entry("e1", null, user("m1", 1)), entry("e20", "e1", user("m20", 20)), entry("e140", "e20", user("m140", 140))], [{ id: "e1", seq: seqs[0] }, { id: "e20", seq: seqs[19] }, { id: "e140", seq: seqs[139] }]);
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.notEqual(entries[0].message, seen[0], "m1 was evicted");
  assert.equal(entries[1].message, seen[19], "m20 is inside the bound");
  assert.equal(entries[2].message, seen[139], "m140 is the newest");
}
`)
}

// upstream: agent-session.ts _entryIdsByMessage is a WeakMap: the messages of a session that was switched away from are collectable.
//
// The table of message objects a process keeps (messageObjects) holds up to 128 of them strongly, so a session switch drops it (the numbers never repeat, so nothing a later session numbers can find an old object). The first runtime to see the new session id drops it and the others of the process do not: the first message after a switch is numbered by every extension of a cell in turn, and they still share its object.
func TestNodeRuntimeReleasesTheMessagesOfAPreviousSession(t *testing.T) {
	nodeIdentityScript(t, "node message objects follow the session", `
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const collect = async () => { for (let i = 0; i < 4; i++) { await settle(); gc(); } };

// (a) The object the process holds for a message of s1 is released by the switch to s2.
{
  hostLog = [];
  const runtime = newRuntime();
  let ref;
  runtime.on("message_end", (event) => { ref = new WeakRef(event.message); });
  push(runtime, [], [], "s1");
  await messageEnd(runtime, user("x".repeat(1 << 20)), nextSeq());
  assert.ok(ref.deref(), "the process holds the message of the current session");
  await collect();
  assert.ok(ref.deref(), "still held while the session is current");
  hostLog = [];
  push(runtime, [], [], "s2");
  await collect();
  assert.equal(ref.deref(), undefined, "the message of the replaced session was released");
}

// (b) Two runtimes of one process: the one that sees the new session second does not drop what the first one already numbered.
{
  hostLog = [];
  const first = newRuntime(), second = newRuntime();
  const seen = [];
  first.on("message_start", (event) => { seen.push(event.message); });
  second.on("message_start", (event) => { seen.push(event.message); });
  push(first, [], [], "s1");
  push(second, [], [], "s1");
  hostLog = [];
  push(first, [], [], "s2");
  const seq = nextSeq();
  await emit(first, "message_start", 1, { message: user() }, [seq]);
  push(second, [], [], "s2");
  await emit(second, "message_start", 1, { message: user() }, [seq]);
  assert.equal(seen[0], seen[1], "both extensions of the cell hold one object for the first message after the switch");
}
`)
}

// upstream: runner.ts emitMessageEnd hands every handler one event.message, and agent-session.ts appendMessage stores that object: a handler that edits event.message in place (redaction extensions do) edits the message every later handler, turn_end, agent_end and the saved entry hold.
//
// The host cannot share the object, so the reply carries the message the handler left, when it differs from the one it received (as a tool_call reply carries the edited input), and the host writes it into the message. The test plays the host: a later payload carries the message the reply named.
func TestNodeRuntimeMessageEndInPlaceEditReachesTheHost(t *testing.T) {
	nodeIdentityScript(t, "node message_end in-place edit", `
const edit = { type: "text", text: "(edited)" };
const wire = (...blocks) => (message) => ({ ...message, content: [...message.content, ...blocks] });
const withEdit = wire(edit);

// What the host sends later: the message as the reply left it, or as the host had it when the reply named no edit.
function hostValue(reply, message) {
  return reply?.result?._pigMessageEndMessage ?? message;
}

{
  hostLog = [];
  const runtime = newRuntime();
  const replies = [];
  runtime.conn.respond = (id, result, error) => replies.push({ id, result, error });
  const first = [], second = [], turns = [], agents = [];
  runtime.on("message_end", (event) => { first.push(event.message); event.message.content.push({ ...edit }); });
  runtime.on("message_end", (event) => { second.push(event.message); });
  runtime.on("turn_end", (event) => { turns.push(event); });
  runtime.on("agent_end", (event) => { agents.push(event); });
  const userSeq = nextSeq(), assistantSeq = nextSeq();
  const messages = [[user(), userSeq], [assistant(), assistantSeq]];
  const stored = [];
  for (const [i, [message, seq]] of messages.entries()) {
    await messageEnd(runtime, message, seq, 1);
    const reply = replies.pop();
    assert.deepEqual(reply.result?._pigMessageEndMessage, withEdit(message), "the reply names the message the handler left");
    const current = hostValue(reply, message);
    await messageEnd(runtime, current, seq, 2);
    assert.equal(second[i], first[i], "the next handler holds the first one's object");
    assert.equal(second[i].content.length, 2, "and the edit is in it");
    stored.push(current);
    push(runtime, [entry("e" + i, i === 0 ? null : "e" + (i - 1), current)], [{ id: "e" + i, seq }]);
    replies.length = 0;
  }
  await emit(runtime, "turn_end", 3, { turnIndex: 0, message: stored[1], toolResults: [] }, [assistantSeq]);
  await emit(runtime, "agent_end", 4, { messages: stored }, [userSeq, assistantSeq]);
  assert.equal(turns[0].message, first[1], "turn_end holds the message_end object");
  assert.equal(turns[0].message.content.length, 2, "with the edit");
  assert.deepEqual(agents[0].messages.map((message, i) => message === first[i]), [true, true], "agent_end holds the message_end objects");
  assert.deepEqual(agents[0].messages.map((message) => message.content.length), [2, 2], "with the edit");
  const entries = runtime.ctx.sessionManager.getEntries();
  assert.deepEqual(entries.map((e, i) => e.message === first[i]), [true, true], "the entries hold the message_end objects");
  assert.deepEqual(entries.map((e) => e.message.content.length), [2, 2], "with the edit");
}

// A handler that leaves the message as it was sends no edit, and one that throws after editing still does: Pi's object holds the edit whatever the handler does next.
{
  hostLog = [];
  const runtime = newRuntime();
  const replies = [];
  runtime.conn.respond = (id, result, error) => replies.push({ id, result, error });
  runtime.on("message_end", () => {});
  runtime.on("message_end", (event) => { event.message.content.push({ ...edit }); throw new Error("after the edit"); });
  await messageEnd(runtime, user(), nextSeq(), 1);
  assert.equal(replies[0].result?._pigMessageEndMessage, undefined, "no edit, no message in the reply");
  assert.equal(replies[0].error, null);
  await messageEnd(runtime, user(), nextSeq(), 2);
  assert.deepEqual(replies[1].result?._pigMessageEndMessage, withEdit(user()), "the edit reaches the host although the handler threw");
  assert.ok(replies[1].error, "and so does the error");
}

// A handler that edits in place and returns a replacement sends both.
{
  const runtime = newRuntime();
  const replies = [];
  runtime.conn.respond = (id, result, error) => replies.push({ id, result, error });
  runtime.on("message_end", (event) => { event.message.content.push({ ...edit }); return { message: { ...event.message, content: [{ type: "text", text: "replacement" }] } }; });
  await messageEnd(runtime, user(), nextSeq());
  assert.deepEqual(replies[0].result._pigMessageEndMessage, withEdit(user()));
  assert.equal(replies[0].result._pigMessageEndResult.message.content[0].text, "replacement");
}
`)
}

// upstream: a handler of message_start, turn_end or agent_end holds the one object the entry stores, so an edit it makes (a block, a property tag) is on the message every later payload and the entry show.
//
// Only a message_end handler's edit goes back to the host. An edit made elsewhere stays in the object the process holds, and a later payload that carries the value the host last sent does not undo it. (A payload that carries another value, a replacement, still does.)
func TestNodeRuntimeKeepsAnEditMadeOutsideMessageEnd(t *testing.T) {
	nodeIdentityScript(t, "node edit outside message_end", `
hostLog = [];
const runtime = newRuntime();
push(runtime, [], []);
let held;
const ends = [], agents = [];
runtime.on("message_start", (event) => { held = event.message; event.message.content.push({ type: "text", text: "(start)" }); event.message.tag = "kept"; });
runtime.on("message_end", (event) => { ends.push(event.message); });
runtime.on("agent_end", (event) => { agents.push(event.messages[0]); });
const seq = nextSeq();
await emit(runtime, "message_start", 1, { message: user() }, [seq]);
await messageEnd(runtime, user(), seq, 2);
assert.equal(ends[0], held);
assert.equal(held.tag, "kept", "message_end's payload carries the host's value again, which is not an edit to undo");
assert.equal(held.content.length, 2);
push(runtime, [entry("e0", null, user())], [{ id: "e0", seq }]);
assert.equal(runtime.ctx.sessionManager.getEntries()[0].message, held, "the entry holds the object");
assert.equal(held.tag, "kept", "the entry shows the value the host sent: the edit stays");
await emit(runtime, "agent_end", 3, { messages: [user()] }, [seq]);
assert.equal(agents[0], held);
assert.equal(held.content.length, 2);

// A value the host changed is applied.
await messageEnd(runtime, { ...user(), content: [{ type: "text", text: "replaced" }] }, seq, 2);
assert.equal(held.content[0].text, "replaced");
assert.equal(held.tag, undefined, "the replacement is the whole message");
`)
}
