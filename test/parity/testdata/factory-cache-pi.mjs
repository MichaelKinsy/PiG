import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { pathToFileURL } from "node:url";
const packageRoot = resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
if (JSON.parse(readFileSync(join(packageRoot, "package.json"), "utf8")).version !== "0.87.1") throw new Error("unexpected Pi version");
const { loadExtensions } = await import(pathToFileURL(join(packageRoot, "dist/core/extensions/loader.js")));
const entry = resolve("test/parity/scenarios/extensions-runtime/testdata/factory-cache/probe.mjs");
for (const mode of ["same-cwd", "direct", "reload", "cross-cwd"]) {
  const root = mkdtempSync(join(tmpdir(), "factory-cache-"));
  try {
    process.env.PIG_FACTORY_CACHE_ROOT = root;
    process.env.PIG_FACTORY_CACHE_CASE = mode;
    process.env.PIG_FACTORY_CACHE_DIST = "dist";
    const result = await loadExtensions([entry], root);
    if (result.errors.length || result.extensions.length !== 1) throw new Error(JSON.stringify(result.errors));
    console.log(`${mode}:${result.extensions[0].commands.get("cache-report").description}`);
    result.runtime.invalidate();
  } finally {
    delete process.env.PIG_FACTORY_CACHE_ROOT;
    delete process.env.PIG_FACTORY_CACHE_CASE;
    delete process.env.PIG_FACTORY_CACHE_DIST;
    rmSync(root, {recursive:true, force:true});
  }
}
