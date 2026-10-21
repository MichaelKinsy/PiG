package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's ctx.getSystemPrompt(), ctx.getSystemPromptOptions() and ctx.isIdle() read the Session (agent-session.ts:1474-1476, 3495-3496, 2344-2349), so a read in the handler that wrote the loadout or sent a message answers the write. The Node runtime replicates the values the Host pushes with the next event; the Host's answer replaces them once the extension has written the loadout, and, inside an agent_settled handler, after a triggering sendMessage for isIdle.
func TestNodeRuntimeReadsAnswerTheExtensionsOwnWrites(t *testing.T) {
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;

const bound = () => {
  const runtime = new Runtime("/ext/replicated.mjs");
  runtime.conn = {};
  runtime.hostReady = true;
  const host = { prompt: "host prompt", options: { selectedTools: ["host"] }, idle: true };
  const sent = [];
  runtime.callSync = (method) => {
    sent.push(method);
    if (method === "getSystemPrompt") return { prompt: host.prompt };
    if (method === "getSystemPromptOptions") return host.options;
    if (method === "isIdle") return { idle: host.idle };
    return {};
  };
  runtime.fireAndForget = (method) => { sent.push(method); };
  runtime.writeSync = (method) => { sent.push(method); };
  runtime.state.systemPrompt = "pushed prompt";
  runtime.state.systemPromptOptions = { selectedTools: ["pushed"] };
  return { runtime, host, sent };
};
const tool = { name: "written", label: "written", description: "written", parameters: { type: "object", properties: {} }, execute: async () => ({ content: [] }) };

// Before a write the replicated values answer, with no host call.
{
  const { runtime, sent } = bound();
  assert.equal(runtime.ctx.getSystemPrompt(), "pushed prompt");
  assert.deepEqual(runtime.ctx.getSystemPromptOptions(), { selectedTools: ["pushed"] });
  assert.deepEqual(sent, []);
}

// setActiveTools and registerTool change what the Host builds the prompt from.
for (const [name, write] of [["setActiveTools", (api) => api.setActiveTools(["host"])], ["registerTool", (api) => api.registerTool(tool)]]) {
  const { runtime, host } = bound();
  write(runtime.api);
  assert.equal(runtime.ctx.getSystemPrompt(), "host prompt", name);
  assert.deepEqual(runtime.ctx.getSystemPromptOptions(), { selectedTools: ["host"] }, name);
  host.prompt = "later prompt";
  assert.equal(runtime.ctx.getSystemPrompt(), "later prompt", name + " again");
}

// A triggering sendMessage starts the run before it returns, so an idle session reads busy, unless the Session holds the message back (agent_settled), which the Host reports.
{
  const { runtime, host } = bound();
  runtime.state.isIdle = true;
  host.idle = true;
  runtime.api.sendMessage({ customType: "note", content: "go", display: true }, { triggerTurn: true });
  assert.equal(runtime.ctx.isIdle(), false, "outside agent_settled");
}
for (const idle of [false, true]) {
  const { runtime, host } = bound();
  runtime.state.isIdle = !idle;
  host.idle = idle;
  runtime.requestContext.run({ id: "settled", event: "agent_settled", settled: false }, () => {
    runtime.api.sendMessage({ customType: "note", content: "go", display: true }, { triggerTurn: true });
  });
  assert.equal(runtime.ctx.isIdle(), idle, "agent_settled, host idle " + idle);
}
`, (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("replicated reads: %v\n%s", err, output)
	}
}
