//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A process with no extension runtime reaches PiG's providers through `pig provider-bridge` (shims/provider-bridge-client.mjs). The fake bridge below speaks the wire the Go bridge
// speaks (coding/cli/provider_bridge.go) so each contract of the client is checked alone: provider order, one terminal outcome per stream id, cancellation, callbacks that run in the
// caller, the bridge ending under a live stream, and a process that exits when no stream is in flight.
const fakeProviderBridge = `
import { createInterface } from "node:readline";
const rl = createInterface({ input: process.stdin });
const send = (o) => process.stdout.write(JSON.stringify(o) + "\n");
const pending = new Map();
rl.on("line", (line) => {
  const r = JSON.parse(line);
  if (r.type === "callbackResult") { pending.get(r.id + "/" + r.callbackId)?.(r); return; }
  if (r.type === "cancel") { send({ id: r.id, type: "event", event: { type: "error", reason: "aborted", error: { role: "assistant", content: [], stopReason: "aborted", errorMessage: "cancelled" } } }); send({ id: r.id, type: "result", result: {} }); return; }
  const message = (text) => ({ role: "assistant", content: [{ type: "text", text }], stopReason: "stop", api: "openai-completions", provider: "p", model: "m", usage: { input: 1, output: 1, cacheRead: 0, cacheWrite: 0, totalTokens: 2, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, timestamp: 1 });
  const mode = r.request.messages[0].content;
  if (mode === "die") { send({ id: r.id, type: "event", event: { type: "start", partial: message("") } }); process.exit(3); }
  if (mode === "hold") { send({ id: r.id, type: "event", event: { type: "start", partial: message("") } }); return; }
  if (mode === "callbacks") {
    const ask = (callbackId, callback, value) => new Promise((resolve) => { pending.set(r.id + "/" + callbackId, resolve); send({ id: r.id, type: "callback", callback, callbackId, value }); });
    (async () => {
      const payload = await ask(1, "onPayload", { a: 1 });
      const headers = await ask(2, "transformHeaders", { h: "1" });
      send({ id: r.id, type: "event", event: { type: "done", reason: "stop", message: message(JSON.stringify({ payload: payload.result, headers: headers.result, failure: payload.failure })) } });
      send({ id: r.id, type: "result", result: message("") });
    })();
    return;
  }
  send({ id: r.id, type: "event", event: { type: "start", partial: message("") } });
  send({ id: r.id, type: "event", event: { type: "done", reason: "stop", message: message("pong:" + r.simple + ":" + r.request.apiKey) } });
  send({ id: r.id, type: "result", result: message("pong") });
});
`

const providerBridgeClientScript = `
const { bridgeStream } = await import("file://" + process.argv[2]);
const failed = (model, message) => ({ failedWith: message });
const model = { provider: "p", id: "m", api: "openai-completions" };
const ctx = (content) => ({ messages: [{ role: "user", content, timestamp: 0 }] });
const out = {};
const drain = async (stream) => { const types = []; for await (const e of stream) types.push(e.type); return types; };

const ok = bridgeStream(model, ctx("ok"), { apiKey: "K" }, true, failed);
out.order = await drain(ok);
out.text = (await ok.result()).content[0].text;

const seen = [];
const callbacks = bridgeStream(model, ctx("callbacks"), { onPayload: (p) => { seen.push(p); return { replaced: true }; }, transformHeaders: (h) => ({ ...h, extra: "x" }) }, true, failed);
await drain(callbacks);
out.callbackText = (await callbacks.result()).content[0].text;
out.callbackSeen = seen;
const rejecting = bridgeStream(model, ctx("callbacks"), { onPayload: () => { throw new Error("hook failed"); }, transformHeaders: (h) => h }, true, failed);
await drain(rejecting);
out.rejectedText = (await rejecting.result()).content[0].text;

const controller = new AbortController();
const held = bridgeStream(model, ctx("hold"), { signal: controller.signal }, true, failed);
const events = [];
const collecting = (async () => { for await (const e of held) events.push(e.type); })();
await new Promise((resolve) => setTimeout(resolve, 200));
controller.abort();
await collecting;
out.abortedEvents = events;
out.abortedStop = (await held.result()).stopReason;

const dying = bridgeStream(model, ctx("die"), {}, true, failed);
out.dyingEvents = await drain(dying);
const dyingResult = await dying.result();
out.dyingStop = dyingResult.stopReason;
out.dyingMessage = dyingResult.errorMessage;

out.fetch = bridgeStream(model, ctx("ok"), { fetch: () => {} }, true, failed).failedWith;
const binary = process.env.PIG_HARNESS_BINARY;
delete process.env.PIG_HARNESS_BINARY;
out.noBinary = bridgeStream(model, ctx("ok"), {}, true, failed).failedWith;
process.env.PIG_HARNESS_BINARY = binary;

// A later request starts a new bridge; once its stream ends, that live, idle bridge must not keep this process alive.
const again = bridgeStream(model, ctx("ok"), { apiKey: "K2" }, true, failed);
await drain(again);
out.againText = (await again.result()).content[0].text;
console.log(JSON.stringify(out));
`

func TestProviderBridgeClientHonoursTheBridgeWire(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	runtime := t.TempDir()
	if err := extractNodeRuntime(runtime); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	fake := write("fake-bridge.mjs", fakeProviderBridge, 0o644)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := write("client.mjs", providerBridgeClientScript, 0o644)
	command := exec.Command(node, script, filepath.Join(runtime, "shims", "provider-bridge-client.mjs"))
	command.Env = append(os.Environ(), "PIG_HARNESS_BINARY="+binary, fakeProviderBridgeNodeEnv+"="+node, fakeProviderBridgeScriptEnv+"="+fake)
	done := make(chan struct{})
	var output []byte
	go func() {
		defer close(done)
		output, err = command.Output()
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = command.Process.Kill()
		t.Fatal("the client did not finish: a bridge in flight keeps its process alive, or a stream never ended")
	}
	if err != nil {
		var stderr []byte
		if exit, ok := errors.AsType[*exec.ExitError](err); ok {
			stderr = exit.Stderr
		}
		t.Fatalf("client script: %v\n%s\n%s", err, output, stderr)
	}
	var got struct {
		Order         []string `json:"order"`
		Text          string   `json:"text"`
		CallbackText  string   `json:"callbackText"`
		CallbackSeen  []any    `json:"callbackSeen"`
		RejectedText  string   `json:"rejectedText"`
		AbortedEvents []string `json:"abortedEvents"`
		AbortedStop   string   `json:"abortedStop"`
		DyingEvents   []string `json:"dyingEvents"`
		DyingStop     string   `json:"dyingStop"`
		DyingMessage  string   `json:"dyingMessage"`
		AgainText     string   `json:"againText"`
		_             string   `json:"-"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("output %q: %v", output, err)
	}
	var raw map[string]any
	_ = json.Unmarshal(output, &raw)
	if len(got.Order) != 2 || got.Order[0] != "start" || got.Order[1] != "done" || got.Text != "pong:true:K" {
		t.Errorf("events %v result %q: want start, done in provider order and the simple flag and API key on the wire", got.Order, got.Text)
	}
	if got.CallbackText != `{"payload":{"defined":true,"value":{"replaced":true}},"headers":{"h":"1","extra":"x"}}` || len(got.CallbackSeen) != 1 {
		t.Errorf("callbacks ran as %q seen %v: onPayload answers {defined, value}; transformHeaders its result", got.CallbackText, got.CallbackSeen)
	}
	if got.RejectedText != `{"failure":"hook failed"}` && !strings.Contains(got.RejectedText, "hook failed") {
		t.Errorf("a throwing callback must answer as a failure on the same stream, got %q", got.RejectedText)
	}
	if got.AbortedStop != "aborted" || len(got.AbortedEvents) == 0 || got.AbortedEvents[len(got.AbortedEvents)-1] != "error" {
		t.Errorf("an aborted signal sends cancel and the stream ends aborted: events %v stop %q", got.AbortedEvents, got.AbortedStop)
	}
	if got.DyingStop != "error" || !strings.Contains(got.DyingMessage, "exited") || len(got.DyingEvents) == 0 || got.DyingEvents[len(got.DyingEvents)-1] != "error" {
		t.Errorf("a bridge that exits under a live stream ends that stream with an error: events %v stop %q message %q", got.DyingEvents, got.DyingStop, got.DyingMessage)
	}
	if got.AgainText != "pong:true:K2" {
		t.Errorf("a request after the bridge exited starts a new bridge, got %q", got.AgainText)
	}
	if !strings.Contains(str(raw["fetch"]), "fetch cannot be used") || !strings.Contains(str(raw["noBinary"]), "PIG_HARNESS_BINARY") {
		t.Errorf("fetch %v, missing binary %v", raw["fetch"], raw["noBinary"])
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
