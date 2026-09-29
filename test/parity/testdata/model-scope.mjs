import { execFileSync } from "node:child_process";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

if (process.argv[2] === "pig") {
  process.stdout.write(execFileSync("go", ["run", "./test/parity/testdata/model-scope-go"], { encoding: "utf8" }));
} else {
  const root = process.env.PI_PACKAGE_ROOT;
  const { createAgentSession, ModelRuntime, SessionManager, SettingsManager } = await import(pathToFileURL(join(root, "dist/index.js")));
  const { AuthStorage } = await import(pathToFileURL(join(root, "dist/core/auth-storage.js")));
  const { InMemoryModelsStore } = await import(pathToFileURL(join(root, "node_modules/@earendil-works/pi-ai/dist/models-store.js")));
  const dir = await mkdtemp(join(tmpdir(), "model-scope-"));
  const snapshots = [];
  try {
    for (const [i, [scoped, persist]] of [[true, true], [false, true], [true, false]].entries()) {
      const cwd = join(dir, String(i));
      await mkdir(cwd);
      const modelRuntime = await ModelRuntime.create({ credentials: AuthStorage.inMemory({ anthropic: { type: "api_key", key: "test-key" } }), modelsStore: new InMemoryModelsStore(), modelsPath: null, allowModelNetwork: false });
      const sonnet = modelRuntime.getModel("anthropic", "claude-sonnet-4-5");
      const opus = modelRuntime.getModel("anthropic", "claude-opus-4-8");
      const settingsManager = SettingsManager.create(cwd, cwd);
      if (scoped) settingsManager.setEnabledModels(["anthropic/claude-sonnet-4-5"]);
      const { session } = await createAgentSession({ cwd, agentDir: cwd, modelRuntime, model: sonnet, noTools: "all", scopedModels: scoped ? [{ model: sonnet }] : [], settingsManager, sessionManager: SessionManager.inMemory(cwd) });
      try {
        await session.setModel(opus, { persist });
        snapshots.push({ scope: session.scopedModels.map(({ model }) => `${model.provider}/${model.id}`), enabled: settingsManager.getEnabledModels() ?? null, defaultProvider: settingsManager.getDefaultProvider() ?? "", defaultModel: settingsManager.getDefaultModel() ?? "" });
      } finally { session.dispose(); }
    }
    console.log(JSON.stringify(snapshots));
  } finally { await rm(dir, { recursive: true, force: true }); }
}
