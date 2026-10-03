package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNodeRuntimeSDKSurfaceAdditions exercises the Pi surfaces the Node
// runtime gained with docs/extension-sdk-surface.md: tool promptSnippet on the
// register frame, ctx.compact's flat options and onComplete/onError,
// newSession/fork's flat options, ctx.thinkingLevel, ui.getEditorComponent,
// the AbortSignal on session_before_compact/session_before_tree events, and
// ctx.ui.theme's name and sourcePath.
func TestNodeRuntimeSDKSurfaceAdditions(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Runtime, requestSignal } from %q;

// register: promptSnippet travels as prompt_snippet.
const sock = process.platform === "win32"
  ? "\\\\.\\pipe\\pig-sdk-surface-" + process.pid
  : join(tmpdir(), "pig-sdk-surface-" + process.pid + ".sock");
const frame = new Promise((resolve, reject) => {
  const server = net.createServer((socket) => {
    let buffer = Buffer.alloc(0);
    socket.on("data", (chunk) => {
      buffer = Buffer.concat([buffer, chunk]);
      if (buffer.length < 4) return;
      const size = buffer.readUInt32BE(0);
      if (buffer.length < 4 + size) return;
      resolve(JSON.parse(buffer.subarray(4, 4 + size).toString("utf8")));
      socket.destroy();
      server.close();
    });
  });
  server.on("error", reject);
  server.listen(sock);
});
process.env.PIG_EXT_SOCKET = sock;
const registering = new Runtime("/ext/surface.mjs");
registering.api.registerTool({
  name: "snippet_tool", label: "Snippet", description: "d", promptSnippet: "Look things up",
  parameters: { type: "object", properties: {} }, execute: async () => ({ content: [] }),
});
await registering.connect();
const register = (await frame).register;
assert.equal(register.tools[0].prompt_snippet, "Look things up");
assert.equal(register.tools[0].label, "Snippet");
registering.conn.socket.destroy();

const runtime = new Runtime("/ext/surface.mjs");
const fired = [];
runtime.fireAndForget = (method, args) => { fired.push([method, args]); };
const calls = [];
let answer = { summary: "compacted", firstKeptEntryId: "e2", tokensBefore: 10 };
runtime.conn = {
  call: async (method, args, parent) => {
    calls.push([method, args, parent]);
    if (answer instanceof Error) throw answer;
    return answer;
  },
  callSync(method) { assert.equal(method, "ui.autocomplete.current"); return {id:"base",triggerCharacters:[],hasFileTrigger:false}; },
  requestState() {},
  notify() {},
};

// ctx.compact without callbacks: the host reads the options flat.
runtime.ctx.compact({ customInstructions: "keep the plan" });
assert.deepEqual(fired.at(-1), ["compact", { customInstructions: "keep the plan" }]);

// ctx.compact with callbacks: an unparented call that awaits completion.
const completed = await new Promise((resolve) => runtime.ctx.compact({ customInstructions: "x", onComplete: resolve }));
assert.deepEqual(calls.at(-1), ["compact", { customInstructions: "x", awaitCompletion: true }, ""]);
assert.equal(completed.summary, "compacted");
answer = new Error("Nothing to compact");
const failed = await new Promise((resolve) => runtime.ctx.compact({ onError: resolve }));
assert.equal(failed.message, "Nothing to compact");

// newSession and fork send their options flat, as the host decodes them.
answer = { cancelled: false };
assert.deepEqual(await runtime.ctx.newSession({ parentSession: "/s/parent.jsonl" }), { cancelled: false });
assert.deepEqual(calls.at(-1).slice(0, 2), ["newSession", { parentSession: "/s/parent.jsonl" }]);
await runtime.ctx.fork("entry-1", { position: "at" });
assert.deepEqual(calls.at(-1).slice(0, 2), ["fork", { position: "at", entryId: "entry-1" }]);

// A tool result's usage travels with it.
assert.deepEqual(runtime.normalizeToolResult({ content: "ok", usage: { input: 1, output: 2 } }).usage, { input: 1, output: 2 });

// ctx.thinkingLevel reads the replicated state; a request ctx inherits it.
runtime.state.thinkingLevel = "high";
assert.equal(runtime.ctx.thinkingLevel, "high");
assert.equal(Object.create(runtime.ctx).thinkingLevel, "high");

// ui.getEditorComponent returns the factory this extension set.
const factory = () => ({ render: () => [], setText() {}, getText: () => "", invalidate() {} });
runtime.ui.setEditorComponent(factory);
assert.equal(runtime.ui.getEditorComponent(), factory);
runtime.ui.setEditorComponent(undefined);
assert.equal(runtime.ui.getEditorComponent(), undefined);

// Replacement releases every session-derived cache, including the registry's
// indexed view, even if the extension never queries the new empty session.
runtime.applyState({ session: { sessionId: "old", leafId: "old-entry", entryCount: 1, entriesAppended: [{ type: "message", id: "old-entry", parentId: null }] } });
assert.equal(runtime.ctx.sessionManager.getEntry("old-entry").id, "old-entry");
assert.ok(runtime.ctx.sessionManager._cachedIndex);
runtime.applyState({ session: { sessionId: "new", leafId: "", entryCount: 0 } });
assert.equal(runtime.ctx.sessionManager._cachedIndex, undefined);

// ctx.ui.theme carries the host theme's name and source path.
runtime.ui.theme.setPalette({ name: "night", sourcePath: "/themes/night.json", foregrounds: { accent: "\x1b[36m" }, backgrounds: {}, modifiers: true, mode: "truecolor" });
assert.equal(runtime.ui.theme.name, "night");
assert.equal(runtime.ui.theme.sourcePath, "/themes/night.json");
runtime.ui.theme.setPalette({ name: "dark" });
assert.equal(runtime.ui.theme.sourcePath, undefined);

// session_before_compact and session_before_tree handlers get an AbortSignal.
const responses = [];
runtime.respond = async (id, result, error) => { responses.push([id, result, error]); };
for (const name of ["session_before_compact", "session_before_tree"]) {
  let seen;
  runtime.on(name, (event) => { seen = event.signal; return undefined; });
  const handlerId = runtime.handlers.get(name).at(-1).id;
  const controller = new AbortController();
  const ctx = Object.create(runtime.ctx);
  // event.signal is the compaction or tree operation's signal, carried by the request; ctx.signal is the run's (runner.ts:917-920).
  Object.defineProperty(ctx, requestSignal, { value: controller.signal });
  await runtime.handleRequest("r-" + name, { method: "event", event: name, handler_id: handlerId, args: { preparation: {} } }, ctx);
  assert.equal(seen, controller.signal, name);
}
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("node runtime SDK surface additions: %v\n%s", err, output)
	}
}
