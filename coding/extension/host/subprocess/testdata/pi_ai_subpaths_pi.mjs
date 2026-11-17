// Loads the generated probe extension through the pinned Pi's own extension loader machinery in both resolution modes and prints each specifier's outcome as JSON.
// usage: node pi_ai_subpaths_pi.mjs <alias|virtual> <pi-coding-agent package dir> <extension file>
// alias: the unbundled Node build (loader.ts getAliases, prefix aliases); virtual: the bundled Node distribution, SEA and the Bun binary (VIRTUAL_MODULES, exact specifiers, tryNative false).
import { createRequire } from "node:module";
import path from "node:path";
import { pathToFileURL } from "node:url";

const [mode, agent, ext] = process.argv.slice(2);
if (mode === "alias") {
  const { loadExtensions } = await import(pathToFileURL(path.join(agent, "dist/core/extensions/loader.js")).href);
  const result = await loadExtensions([ext], path.dirname(ext));
  if (result.errors.length) throw new Error(JSON.stringify(result.errors));
  process.stdout.write([...result.extensions[0].commands.values()][0].description);
} else {
  const { VIRTUAL_MODULES } = await import(pathToFileURL(path.join(agent, "dist/core/extensions/virtual-modules.js")).href);
  const { createJiti } = createRequire(path.join(agent, "package.json"))("jiti");
  const jiti = createJiti(import.meta.url, { moduleCache: false, virtualModules: VIRTUAL_MODULES, tryNative: false });
  const factory = await jiti.import(ext, { default: true });
  let description;
  await factory({ registerCommand: (_name, options) => { description = options.description; } });
  process.stdout.write(description);
}
