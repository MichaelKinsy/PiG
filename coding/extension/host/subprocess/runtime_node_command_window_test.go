package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi reads stdin's end in the iteration after the line that starts a command only when the client closes stdin at once; a client that closes it later finds every continuation that ran in between answered (rpc-mode.ts:802-805, agent-session.ts:1904-1908). The Node runtime therefore reports a command's closed window as a suspension only after runtime_input_end, and gives a command that started or finished a host call since its window closed a fresh window. Each case runs in a Node process of its own because the drain state is per process.
func TestNodeRuntimeReportsCommandSuspensionAfterInputEnd(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	harness := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
const frames = [];
const runtime = new Runtime("/ext/window.mjs");
const queue = [];
let wake;
runtime.connect = async () => {
  const conn = runtime.attachConnection({ on() {}, off() {}, write() { return true; }, waitUntil() {} });
  conn.send = (env) => { frames.push(env); return true; };
  conn.next = () => (queue.length ? Promise.resolve(queue.shift()) : new Promise((resolve) => { wake = resolve; }));
  runtime.conn = conn;
};
const feed = (env) => { if (wake) { const w = wake; wake = undefined; w(env); } else queue.push(env); };
const turn = async () => { for (let i = 0; i < 6; i++) await new Promise((resolve) => setImmediate(resolve)); await new Promise((resolve) => setTimeout(resolve, 5)); };
const states = () => frames.filter((f) => f.type === "request_state").map((f) => f.request_state.state).filter((s) => s === "suspended");
const responses = () => frames.filter((f) => f.type === "response").length;
const answer = (method) => feed({ type: "call_result", id: frames.find((f) => f.type === "call" && f.call.method === method).id, call_result: { result: null } });
const inputEnd = () => feed({ type: "notify", notify: { method: "runtime_input_end" } });
const start = (handler) => {
  runtime.commands.set("cmd", { handler });
  void runtime.run();
  feed({ type: "ready", ready: { mode: "rpc", models: [], cwd: "/" } });
  feed({ type: "request", id: "r1", request: { method: "command", tool: "cmd", args: "" } });
};
%s
process.exit(0);
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String(), "%s")
	cases := []struct{ name, body string }{
		{"a window closed on a pending call and nothing since is suspended at input end", `
start(async (_args, ctx) => { await ctx.waitForIdle(); });
await turn();
assert.equal(states().length, 0, "no suspension before input ended");
inputEnd();
await turn();
assert.equal(states().length, 1, "suspended once input ended");
`},
		{"a command that resumed gets a fresh window and is suspended when it waits again", `
start(async (_args, ctx) => { await ctx.waitForIdle(); await new Promise(() => {}); });
await turn();
answer("waitForIdle");
await turn();
assert.equal(states().length, 0, "no suspension before input ended");
inputEnd();
await turn();
assert.equal(states().length, 1, "suspended when it waits again");
`},
		{"a command that answers before input end is never suspended", `
start(async (_args, ctx) => { await ctx.waitForIdle(); });
await turn();
answer("waitForIdle");
await turn();
assert.equal(responses(), 1);
inputEnd();
await turn();
assert.equal(states().length, 0);
`},
		{"a command that resumed onto a short call keeps its window open until the call settles", `
start(async (_args, ctx) => { await ctx.waitForIdle(); await ctx.ui.setLogin({ providerId: "p" }); });
await turn();
answer("waitForIdle");
await turn();
inputEnd();
await turn();
assert.equal(states().length, 0, "a pending short call keeps the window open");
answer("ui.setLogin");
await turn();
assert.equal(responses(), 1);
assert.equal(states().length, 0);
`},
		{"a timer that ran after the window closed is not a host call, so the command is suspended at input end", `
start(async (_args, ctx) => { void ctx.waitForIdle(); await new Promise((resolve) => setTimeout(resolve, 10)); await new Promise(() => {}); });
await turn();
await new Promise((resolve) => setTimeout(resolve, 40));
assert.equal(states().length, 0, "no suspension before input ended");
inputEnd();
await turn();
assert.equal(states().length, 1, "the second timer is pending at input end, as in Pi");
assert.equal(responses(), 0);
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := fmt.Sprintf(harness, tc.body)
			if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}
