package subprocess

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream builds ExtensionContext (runner.ts createContext) and
// ExtensionUIContext (interactive-mode.ts createExtensionUIContext) from arrow
// functions, so a method keeps working when an extension detaches it.
// pi-lens calls `const setWidget = ui.setWidget; setWidget(...)` on every
// turn_start and passes `ctx.ui.setStatus` along for its "LSP Inactive"
// footer status.
func TestNodeContextAndUIMethodsWorkDetached(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const runtime = new Runtime("/ext/detached.mjs");
const sent = [];
runtime.fireAndForget = (method, args) => { sent.push([method, args]); };
runtime.state.isIdle = false;
runtime.state.projectTrusted = true;

const { setStatus, setWidget, notify, getEditorText } = runtime.ui;
setStatus("pi-lens-lsp", "LSP Inactive");
notify("hello", "info");
setWidget("pi-lens", undefined);
assert.equal(getEditorText(), "");
assert.deepEqual(sent.map(([method]) => method), ["ui.setStatus", "ui.notify", "ui.setWidget"]);
assert.equal(runtime.state.footerData.extensionStatuses["pi-lens-lsp"], "LSP Inactive");

const { isIdle, isProjectTrusted, getSystemPromptOptions } = runtime.ctx;
assert.equal(isIdle(), false);
assert.equal(isProjectTrusted(), true);
assert.deepEqual(getSystemPromptOptions(), {});

// A per-request context still reads the live runtime through bound methods.
const requestCtx = Object.create(runtime.ctx);
// ctx.signal is a getter of the run's signal (runner.ts:917-920), never assigned.
assert.equal(requestCtx.isIdle(), false);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("detached ctx and ui methods: %v\n%s", err, output)
	}
}

// Upstream reports a throwing handler's `err.stack`, which interactive mode
// prints dimmed under the error line. The runtime sends the stack with the
// error, and the host hands it to the runner as the error's own stack.
func TestNodeHandlerErrorCarriesItsStack(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { errorInfo } from %q;
function onTurnStart() { return undefined.runtime; }
let thrown;
try { onTurnStart(); } catch (err) { thrown = err; }
const info = errorInfo(thrown);
assert.equal(info.message, "Cannot read properties of undefined (reading 'runtime')");
assert.equal(info.stack, thrown.stack);
assert.match(info.stack, /at onTurnStart/);
assert.deepEqual(errorInfo("plain"), { message: "plain" });
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("handler error stack on the wire: %v\n%s", err, output)
	}

	const stack = "TypeError: boom\n    at onTurnStart (file:///ext/index.js:10:5)"
	hostEnd, peer := net.Pipe()
	conn := NewConn("thrower", hostEnd)
	conn.Start(t.Context())
	managed := withConn(&managedExt{config: ExtConfig{Name: "thrower"}, host: NewHost(t.TempDir())}, conn)
	handler := managed.makeEventHandler("turn_start", 1)
	done := make(chan error, 1)
	go func() {
		_, err := handler(map[string]any{"type": "turn_start"}, t.Context())
		done <- err
	}()
	request := readLivenessEnvelope(t, peer)
	writeLivenessEnvelope(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{
		Result: json.RawMessage("null"),
		Error:  &ErrorInfo{Message: "boom", Stack: stack},
	}})
	got := <-done
	_ = peer.Close()
	if got == nil || got.Error() != "boom" || extension.ErrorStack(got) != stack {
		t.Fatalf("handler error = %v with stack %q; want boom with the extension's stack", got, extension.ErrorStack(got))
	}
}

// Pi's extension calls are in-process, so no fire-and-forget call fails when the process shuts down (runner.ts, interactive-mode.ts). A host that closes the connection while a call nobody awaits is in flight ends the run, and a host that cancels the request that made the call cancels the call: the runtime does not write either call to stderr, which print and JSON mode share with the user (--list-models and --help answer and exit while the post-handshake provider.configRef call is still pending). A host that answers the call with an error is still written there.
func TestNodeFireAndForgetReportsHostErrorsButNotShutdown(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { Connection, Runtime } from %q;
const written = [];
process.stderr.write = (chunk) => { written.push(String(chunk)); return true; };
// Like ProviderSocket, a closed socket throws on write.
function fakeSocket() {
  const socket = new EventEmitter();
  socket.sent = [];
  socket.closed = false;
  socket.on("close", () => { socket.closed = true; });
  socket.write = (data) => {
    if (socket.closed) throw new Error("connection closed");
    socket.sent.push(data);
    return true;
  };
  return socket;
}
const unhandled = [];
process.on("unhandledRejection", (reason) => { unhandled.push(String(reason)); });
const settle = () => new Promise((resolve) => setImmediate(resolve));

// The host closes the connection while the call is pending.
let runtime = new Runtime("/ext/shutdown.mjs");
let socket = fakeSocket();
runtime.conn = new Connection(socket);
runtime.fireAndForget("provider.configRef", { name: "p" });
assert.equal(socket.sent.length, 1);
socket.emit("close");
await settle();
// A call made after the close rejects at once.
runtime.fireAndForget("ui.notify", { message: "late" });
await settle();
assert.deepEqual(written, []);

// The host cancels the request that made the call (runtime loop "cancel", shutdown) while the connection stays open.
runtime = new Runtime("/ext/cancelled.mjs");
socket = fakeSocket();
runtime.conn = new Connection(socket);
const request = { id: "req-1", settled: false, pendingHostCalls: new Set() };
runtime.requestContext.run(request, () => runtime.fireAndForget("appendEntry", { customType: "x" }));
assert.equal(runtime.conn.pending.size, 1);
runtime.conn.cancelParent("req-1");
await settle();
assert.equal(runtime.conn.closed, false);
assert.deepEqual(written, []);

// The host closes the connection while a call made by a request is pending, and the request makes another after the close: the request's state is not sent on the closed connection, the call does not throw into the handler, and the request no longer owns either call.
runtime = new Runtime("/ext/inrequest.mjs");
socket = fakeSocket();
runtime.conn = new Connection(socket);
const owner = { id: "req-2", settled: false, pendingHostCalls: new Set() };
runtime.requestContext.run(owner, () => runtime.fireAndForget("ui.notify", { message: "pending" }));
socket.emit("close");
runtime.requestContext.run(owner, () => runtime.fireAndForget("ui.notify", { message: "late" }));
await settle();
assert.equal(owner.pendingHostCalls.size, 0);
assert.deepEqual(unhandled, []);
assert.deepEqual(written, []);

// The host answers a call with an error while the connection stays open.
runtime = new Runtime("/ext/rejected.mjs");
socket = fakeSocket();
runtime.conn = new Connection(socket);
runtime.fireAndForget("ui.notify", { message: "x" });
const frame = socket.sent[0];
const id = JSON.parse(frame.subarray(4).toString()).id;
runtime.conn.resolveCall({ type: "call_result", id, call_result: { error: { message: "refused" } } });
await settle();
assert.deepEqual(written, ["pig: host call ui.notify failed: refused\n"]);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("fire-and-forget host call failure reporting: %v\n%s", err, output)
	}
}
