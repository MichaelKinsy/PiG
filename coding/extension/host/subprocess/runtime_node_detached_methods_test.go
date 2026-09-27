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
requestCtx.signal = new AbortController().signal;
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
	managed := &managedExt{config: ExtConfig{Name: "thrower"}, host: NewHost(t.TempDir()), conn: conn}
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
