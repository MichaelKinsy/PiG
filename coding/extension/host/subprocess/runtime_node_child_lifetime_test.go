package subprocess_test

import "testing"

// Independent SDK Sessions belong to their extension connection, not to the
// transient command that created them or to another packed member's connection.
func TestNodeIndependentSessionLifetimeIsConnectionScoped(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
const sdk = await import(process.argv[2]);
const ai = await import(new URL("./pi-ai.mjs", process.argv[2]));
const { Runtime } = await import(new URL("../runtime.mjs", process.argv[2]));
const { runWithRuntime } = await import(new URL("../state.mjs", process.argv[2]));
const { disposeIndependentSessions } = await import(new URL("../independent-session-owner.mjs", process.argv[2]));
const root = mkdtempSync(join(tmpdir(), "child-lifetime-"));
const owners = [new Runtime("owner-a.mjs"), new Runtime("owner-b.mjs")];
const sessions = [];
const pending = [];
const exited = [];
try {
 const runtime = await sdk.ModelRuntime.create({ authPath: join(root, "auth.json"), modelsPath: join(root, "models.json"), allowModelNetwork: false });
 runtime.registerProvider("lifetime", { api: "openai-completions", apiKey: "key", baseUrl: "http://unused.invalid", models: [{ id: "child", name: "child", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 32768, maxTokens: 512 }], streamSimple: () => {
  const stream = ai.createAssistantMessageEventStream();
  const message = { role: "assistant", api: "openai-completions", provider: "lifetime", model: "child", content: [{ type: "toolCall", id: "wait", name: "wait", arguments: {} }], usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } }, stopReason: "toolUse", timestamp: 1 };
  stream.push({ type: "done", reason: "toolUse", message }); return stream;
 } });
 await runtime.refresh({ allowNetwork: false });
 for (let index = 0; index < owners.length; index++) {
  let enter;
  const entered = new Promise(resolve => { enter = resolve; });
  const settingsManager = sdk.SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false } });
  const resourceLoader = new sdk.DefaultResourceLoader({ cwd: root, agentDir: root, settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true });
  await resourceLoader.reload();
  const { session } = await runWithRuntime(owners[index], () => sdk.createAgentSession({ cwd: root, agentDir: root, modelRuntime: runtime, model: runtime.getModel("lifetime", "child"), sessionManager: sdk.SessionManager.inMemory(root), settingsManager, resourceLoader, tools: ["wait"], customTools: [{ name: "wait", label: "wait", description: "block until abort", parameters: { type: "object", properties: {} }, async execute(_id, _args, signal) {
   enter();
   await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }));
   exited.push(index);
   return { content: [{ type: "text", text: "stopped" }] };
  } }] }));
  sessions.push(session);
  pending.push(runWithRuntime(owners[index], () => session.prompt("wait")));
  await entered;
 }
 await disposeIndependentSessions(owners[0]);
 await pending[0];
 assert.deepEqual(exited, [0]);
 assert.equal(sessions[0].isStreaming, false);
 assert.equal(sessions[1].isStreaming, true);
 await assert.rejects(runWithRuntime(owners[0], () => sdk.createAgentSession()), /connection is closed/);
 await disposeIndependentSessions(owners[1]);
 await pending[1];
 assert.deepEqual(exited, [0, 1]);
 assert.equal(sessions[1].isStreaming, false);
 await disposeIndependentSessions(owners[1]);
} finally {
 for (const session of sessions) session.dispose();
 await Promise.allSettled(pending);
 rmSync(root, { recursive: true, force: true });
}
`)
}
