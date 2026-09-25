import assert from "node:assert/strict";
import { test } from "node:test";
import { realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Load the same extension as Pi, using its installed package to resolve pi-ai.
// The extension finds Pi's package from process.argv[1], which is a file in
// that package when Pi runs it. npm links .bin/pi to the package on POSIX but
// writes a shim script there on Windows, so name a file in the package.
process.argv[1] = realpathSync(fileURLToPath(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json", import.meta.url)));
const { default: register } = await import("./test-faux-provider.ts");
let provider;
register({ registerProvider(_name, definition) { provider = definition; } });

async function ids(sessionId, prompt, history = []) {
  const stream = provider.streamSimple({ id: "faux-1" }, {
    messages: [...history, { role: "user", content: prompt }],
  }, { sessionId });
  const ended = [];
  for await (const event of stream) {
    if (event.type === "toolcall_end") ended.push(event.toolCall.id);
  }
  const result = await stream.result();
  assert.notEqual(result.stopReason, "error", result.errorMessage);
  const calls = result.content.filter(block => block.type === "toolCall").map(call => call.id);
  assert.deepEqual(ended, calls);
  return calls;
}

test("tool IDs count calls across turns and batches, independently per session", async () => {
  assert.deepEqual(await ids("a", "What is 20+22?"), []);
  assert.deepEqual(await ids("a", "Run: expr 20 + 22"), ["call_test_faux_1"]);
  assert.deepEqual(await ids("a", "Run: parallel reads"), ["call_test_faux_2", "call_test_faux_3"]);
  assert.deepEqual(await ids("b", "Run: expr 20 + 22"), ["call_test_faux_1"]);
  assert.deepEqual(await ids("a", "Run: expr 20 + 22"), ["call_test_faux_4"]);
  assert.deepEqual(await ids(undefined, "Run: expr 20 + 22"), ["call_test_faux_1"]);
  assert.deepEqual(await ids(undefined, "Run: expr 20 + 22"), ["call_test_faux_2"]);
});

test("resumed IDs include errored, aborted and orphaned history", async () => {
  for (const stopReason of ["toolUse", "error", "aborted"]) {
    const history = [
      { role: "assistant", stopReason, content: [{ type: "toolCall", id: "call_test_faux_7", name: "bash", arguments: {} }] },
      { role: "toolResult", toolCallId: "call_test_faux_9", toolName: "bash", content: [{ type: "text", text: "orphan" }] },
    ];
    assert.deepEqual(await ids(`resume-${stopReason}`, "Run: expr 20 + 22", history), ["call_test_faux_10"]);
  }
});

test("assistant-only resumed IDs seed the counter without a tool result", async () => {
  for (const stopReason of ["toolUse", "error", "aborted"]) {
    const history = [
      { role: "assistant", stopReason, content: [{ type: "toolCall", id: "call_test_faux_7", name: "bash", arguments: {} }] },
    ];
    assert.deepEqual(await ids(`assistant-only-${stopReason}`, "Run: expr 20 + 22", history), ["call_test_faux_8"]);
  }
});

test("concurrent batches use unique contiguous IDs", async () => {
  const requests = 32;
  const batches = await Promise.all(Array.from({ length: requests }, () => ids("concurrent", "Run: parallel reads")));
  const numbers = batches.flat().map(id => Number(id.replace("call_test_faux_", ""))).sort((a, b) => a - b);
  assert.deepEqual(numbers, Array.from({ length: requests * 2 }, (_, i) => i + 1));
});

test("Model Runtime retains test-faux IDs across history-free and concurrent requests", async () => {
  // Use the pinned Pi runtime and the actual extension, with no disk credentials or network refresh.
  const piRoot = new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url);
  const { ModelRuntime } = await import(new URL("dist/core/model-runtime.js", piRoot).href);
  const { AuthStorage } = await import(new URL("dist/core/auth-storage.js", piRoot).href);
  const runtime = await ModelRuntime.create({
    credentials: AuthStorage.inMemory(), modelsPath: null,
    allowModelNetwork: false, refreshOnCreate: false,
  });
  register(runtime);
  const model = runtime.getModel("test-faux", "faux-1");
  assert.ok(model);
  const request = { messages: [{ role: "user", content: "Run: expr 20 + 22" }] };
  const complete = async (sessionId, context = request) => {
    const result = await runtime.completeSimple(model, context, { sessionId });
    assert.equal(result.stopReason, "toolUse", result.errorMessage);
    return result.content.map(call => call.id);
  };
  assert.deepEqual(await complete("runtime-retained"), ["call_test_faux_1"]);
  assert.deepEqual(await complete("runtime-retained"), ["call_test_faux_2"]);
  assert.deepEqual(await complete("runtime-isolated"), ["call_test_faux_1"]);
  const requests = 32;
  const batches = await Promise.all(Array.from({ length: requests }, () => complete("runtime-concurrent", {
    messages: [{ role: "user", content: "Run: parallel reads" }],
  })));
  for (const batch of batches) {
    assert.equal(batch.length, 2);
    const [first, second] = batch.map(id => Number(id.replace("call_test_faux_", "")));
    assert.equal(second, first + 1);
  }
  const numbers = batches.flat().map(id => Number(id.replace("call_test_faux_", ""))).sort((a, b) => a - b);
  assert.deepEqual(numbers, Array.from({ length: requests * 2 }, (_, i) => i + 1));
});
