package subprocess_test

import "testing"

// packages/coding-agent/test/suite/regressions/7193-event-bus-lifecycle.test.ts:16.
// Preserve the original shared bus, factory/resource loader, two Session.reload calls, captured API error and host-listener survival. Only the harness setup is replaced with the public SDK's in-memory Session.
func TestNodeSessionEventBusLifecycle7193MatchesPi(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "index.js"}, "pi-coding-agent.mjs", `
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
for (const [index, url] of process.argv.slice(1).entries()) {
  const m = await import(url);
  const { loadExtensionFromFactory } = await import(new URL(index === 0 ? "./core/extensions/loader.js" : "./pi-dist/pi-coding-agent/core/extensions/loader.js", url));
  const dir = mkdtempSync(join(tmpdir(), "event-bus-7193-"));
  let session;
  try {
    const eventBus = m.createEventBus();
    let extensionCalls = 0, hostCalls = 0, firstApi;
    const factory = pi => {
      firstApi ??= pi;
      pi.events.on("reload:test", () => extensionCalls++);
    };
    eventBus.on("reload:test", () => hostCalls++);
    const loadExtensions = async () => {
      const runtime = m.createExtensionRuntime();
      const extension = await loadExtensionFromFactory(factory, dir, eventBus, runtime);
      return {extensions:[extension], errors:[], runtime};
    };
    let extensionsResult = await loadExtensions();
    const resourceLoader = {
      getExtensions: () => extensionsResult,
      getSkills: () => ({skills:[], diagnostics:[]}),
      getPrompts: () => ({prompts:[], diagnostics:[]}),
      getThemes: () => ({themes:[], diagnostics:[]}),
      getAgentsFiles: () => ({agentsFiles:[]}),
      getSystemPrompt: () => undefined, getSystemPromptSource: () => undefined,
      getAppendSystemPrompt: () => [], getAppendSystemPromptSources: () => [],
      extendResources() {},
      async reload() { extensionsResult = await loadExtensions(); },
    };
    const modelRuntime = await m.ModelRuntime.create({authPath:join(dir,"auth.json"), modelsPath:join(dir,"models.json"), allowModelNetwork:false});
    ({session} = await m.createAgentSession({cwd:dir, agentDir:dir, modelRuntime, resourceLoader, sessionManager:m.SessionManager.inMemory(dir), settingsManager:m.SettingsManager.inMemory()}));
    await session.bindExtensions({shutdownHandler: () => {}});
    const emit = async () => {
      const extensionBefore = extensionCalls, hostBefore = hostCalls;
      eventBus.emit("reload:test", undefined);
      await new Promise(setImmediate);
      return {extension:extensionCalls-extensionBefore, host:hostCalls-hostBefore};
    };
    assert.deepEqual(await emit(), {extension:1, host:1});
    await session.reload();
    assert.throws(() => firstApi?.getCommands(), /stale after session replacement or reload/);
    assert.deepEqual(await emit(), {extension:1, host:1});
    await session.reload();
    assert.deepEqual(await emit(), {extension:1, host:1});
    session.dispose();
    assert.deepEqual(await emit(), {extension:0, host:1});
  } finally {
    session?.dispose();
    rmSync(dir, {recursive:true, force:true});
  }
}
`)
}
