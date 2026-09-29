import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { getPackageDir } from "@earendil-works/pi-coding-agent";

export default async function (pi) {
  const root = process.env.PIG_FACTORY_CACHE_ROOT;
  const mode = process.env.PIG_FACTORY_CACHE_CASE;
  const cwd = join(root, "project");
  const agentDir = join(root, "agent");
  mkdirSync(cwd, { recursive: true });
  mkdirSync(agentDir, { recursive: true });
  // The upstream tests import private loader files. Each driver supplies their mapped compiled-file location; this does not probe package-root import layout.
  const dist = process.env.PIG_FACTORY_CACHE_DIST ?? "dist";
  const { clearExtensionCache, loadExtensions, loadExtensionsCached } = await import(pathToFileURL(join(getPackageDir(), dist, "core/extensions/loader.js")));
  const { DefaultResourceLoader } = await import(pathToFileURL(join(getPackageDir(), dist, "core/resource-loader.js")));
  delete globalThis.__extensionFactoryCacheTest;
  clearExtensionCache();
  const counting = `
const state = (globalThis.__extensionFactoryCacheTest ??= {});
state.moduleLoads = (state.moduleLoads ?? 0) + 1;
export default function () {
  state.factoryRuns = (state.factoryRuns ?? 0) + 1;
}
`;
  const extensionPath = join(root, "counting.ts");
  writeFileSync(extensionPath, counting, "utf8");
  const results = [];
  let freshExtension;
  let freshRuntime;
  try {
    switch (mode) {
      case "same-cwd": {
        const first = await loadExtensionsCached([extensionPath], cwd);
        const second = await loadExtensionsCached([extensionPath], cwd);
        results.push(first, second);
        freshExtension = first.extensions[0] !== second.extensions[0];
        freshRuntime = first.runtime !== second.runtime;
        break;
      }
      case "direct":
        results.push(await loadExtensions([extensionPath], cwd));
        results.push(await loadExtensions([extensionPath], cwd));
        break;
      case "reload": {
        const extensionDir = join(agentDir, "extensions");
        mkdirSync(extensionDir, { recursive: true });
        writeFileSync(join(extensionDir, "counting.ts"), counting, "utf8");
        const loader = new DefaultResourceLoader({ cwd, agentDir, noSkills: true, noPromptTemplates: true, noThemes: true });
        await loader.reload();
        results.push(loader.getExtensions());
        await loader.reload();
        results.push(loader.getExtensions());
        break;
      }
      case "cross-cwd": {
        const firstCwd = join(root, "first");
        const secondCwd = join(root, "second");
        mkdirSync(firstCwd, { recursive: true });
        mkdirSync(secondCwd, { recursive: true });
        results.push(await loadExtensionsCached([extensionPath], firstCwd));
        results.push(await loadExtensionsCached([extensionPath], secondCwd));
        results.push(await loadExtensionsCached([extensionPath], secondCwd));
        break;
      }
      default:
        throw new Error(`unknown cache case: ${mode}`);
    }
    for (const result of results) {
      if (result.errors.length || result.extensions.length !== 1) throw new Error(JSON.stringify(result.errors));
    }
    pi.registerCommand("cache-report", {
      description: JSON.stringify({ ...globalThis.__extensionFactoryCacheTest, freshExtension, freshRuntime }),
      handler: async () => {},
    });
  } finally {
    for (const result of results) result.runtime.invalidate();
    delete globalThis.__extensionFactoryCacheTest;
    clearExtensionCache();
  }
}
