// Profile the independent Session composition used by Node packages.
// node --expose-gc --cpu-prof --heap-prof test/parity/unit-evidence/profile-node-session.mjs
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { performance } from "node:perf_hooks";
import * as sdk from "../../../coding/extension/host/subprocess/runtime-node/shims/pi-coding-agent.mjs";
import { Runtime } from "../../../coding/extension/host/subprocess/runtime-node/runtime.mjs";
import { runWithRuntime } from "../../../coding/extension/host/subprocess/runtime-node/state.mjs";
import { disposeIndependentSessions } from "../../../coding/extension/host/subprocess/runtime-node/shims/independent-session.mjs";

const root = mkdtempSync(join(tmpdir(), "profile-node-session-"));
const owner = new Runtime("profile.mjs");
try {
  const modelRuntime = await sdk.ModelRuntime.create({ authPath: join(root, "auth.json"), modelsPath: join(root, "models.json"), allowModelNetwork: false });
  const model = { id: "profile", provider: "profile", api: "openai-completions", baseUrl: "http://unused.invalid", name: "Profile", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } };
  const settingsManager = sdk.SettingsManager.inMemory({ compaction: { enabled: false } });
  const resourceLoader = new sdk.DefaultResourceLoader({ cwd: root, agentDir: root, settingsManager, noExtensions: true, noSkills: true, noThemes: true, noPromptTemplates: true, noContextFiles: true, systemPrompt: "Independent profile instructions" });
  await resourceLoader.reload();
  global.gc?.();
  const before = process.memoryUsage();
  const samples = [];
  for (let i = 0; i < 100; i++) {
    const start = performance.now();
    const { session } = await runWithRuntime(owner, () => sdk.createAgentSession({ cwd: root, agentDir: root, modelRuntime, model, settingsManager, resourceLoader, sessionManager: sdk.SessionManager.inMemory(root), tools: ["read", "bash", "edit", "write"] }));
    session.dispose();
    samples.push(performance.now() - start);
  }
  await disposeIndependentSessions(owner);
  global.gc?.();
  samples.sort((a, b) => a - b);
  console.log(JSON.stringify({ node: process.version, pi: sdk.VERSION, iterations: samples.length, p50Ms: samples[Math.floor(samples.length * 0.5)], p95Ms: samples[Math.floor(samples.length * 0.95)], before, after: process.memoryUsage(), liveModelStreams: owner.modelStreams.size, liveModelCallbacks: owner.modelStreamCallbacks.size }, null, 2));
} finally {
  await disposeIndependentSessions(owner);
  rmSync(root, { recursive: true, force: true });
}
