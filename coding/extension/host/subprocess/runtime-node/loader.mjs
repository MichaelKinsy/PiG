import { statSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { cacheImportedModules } from "./compile-cache.mjs";

// Node module hooks for the extension runtime. Extensions load through jiti
// (jiti-loader.mjs), as in Pi, which serves the specifiers below as virtual
// modules. This hook serves the same modules to code that Node imports
// natively instead, such as a worker thread an extension starts or the
// runtime's own tests.

// Pi's CLI discards process warnings (cli/setup.ts). The asynchronous hook thread on older Node releases follows the same policy.
process.emitWarning = () => {};

// Specifiers Pi's extension loader serves from its own bundle
// (core/extensions/virtual-modules.ts). Pi resolves the pi-ai root to its
// compat entry point, so both names share one shim here. The pi-ai/oauth entry
// point is type-only; pi-ai/providers/all and pi-agent-core are Pi's own modules.
export const shims = new Map([
  ["@mariozechner/pi-coding-agent", new URL("./shims/pi-coding-agent.mjs", import.meta.url).href],
  ["@earendil-works/pi-coding-agent", new URL("./shims/pi-coding-agent.mjs", import.meta.url).href],
  ["@mariozechner/pi-tui", new URL("./shims/pi-tui.mjs", import.meta.url).href],
  ["@earendil-works/pi-tui", new URL("./shims/pi-tui.mjs", import.meta.url).href],
  ["@mariozechner/pi-agent-core", new URL("./shims/pi-agent-core.mjs", import.meta.url).href],
  ["@earendil-works/pi-agent-core", new URL("./shims/pi-agent-core.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@earendil-works/pi-ai", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai/compat", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@earendil-works/pi-ai/compat", new URL("./shims/pi-ai.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai/oauth", new URL("./shims/pi-ai-oauth.mjs", import.meta.url).href],
  ["@earendil-works/pi-ai/oauth", new URL("./shims/pi-ai-oauth.mjs", import.meta.url).href],
  ["@mariozechner/pi-ai/providers/all", new URL("./shims/pi-dist/pi-ai/sdk-bundle/providers.js", import.meta.url).href],
  ["@earendil-works/pi-ai/providers/all", new URL("./shims/pi-dist/pi-ai/sdk-bundle/providers.js", import.meta.url).href],
  // Pi aliases these to the TypeBox it ships; shims/typebox*.mjs bundle the
  // same pinned release (automation/gen/vendor-typebox.sh).
  ["typebox", new URL("./shims/typebox.mjs", import.meta.url).href],
  ["typebox/value", new URL("./shims/typebox-value.mjs", import.meta.url).href],
  ["typebox/compile", new URL("./shims/typebox-compile.mjs", import.meta.url).href],
  ["@sinclair/typebox", new URL("./shims/typebox.mjs", import.meta.url).href],
  ["@sinclair/typebox/value", new URL("./shims/typebox-value.mjs", import.meta.url).href],
  ["@sinclair/typebox/compile", new URL("./shims/typebox-compile.mjs", import.meta.url).href],
]);

// Pi imports an ES module extension (an .mjs file, or a .js file under "type": "module") through Node's own import, which keeps the module for the life of the process, so Pi runs an edited ES module extension's old code after /reload. The hooks below record which extension module imports which and the source each evaluation loaded, so that a reload evaluates the edited ES modules of the extensions, and the extension modules that import them, again.

const runtimeURL = new URL("./", import.meta.url).href;
const reloadParam = "pig-reload";
// Node evaluates a module of these formats again under a new URL; it keeps a CommonJS module in its require cache by file name.
const reevaluable = new Set(["module", "module-typescript", "json", "wasm"]);

// imports maps the URL of each evaluation of an extension module to the keys of the extension modules it imported.
const imports = new Map();
// loads maps the key of each extension module (its URL without query or fragment) to its latest evaluation: the URL, the module format and the source stamp Node loaded.
const loads = new Map();
// latest maps the key of each extension module that a reload evaluated again to the URL of that evaluation.
const latest = new Map();

// An extension module is a local file outside the runtime's own directory and outside installed packages.
function extensionModule(url) {
  return url.startsWith("file:") && !url.startsWith(runtimeURL) && !url.includes("/node_modules/");
}

function moduleKey(url) {
  const key = new URL(url);
  key.search = "";
  key.hash = "";
  return key.href;
}

function stampOf(url) {
  try {
    const info = statSync(fileURLToPath(url), { bigint: true });
    return `${info.mtimeNs}:${info.size}`;
  } catch {
    return "";
  }
}

function track(result, parentURL) {
  if (!extensionModule(result.url)) return result;
  const key = moduleKey(result.url);
  if (parentURL && extensionModule(parentURL)) {
    if (!imports.has(parentURL)) imports.set(parentURL, new Set());
    imports.get(parentURL).add(key);
  }
  const url = latest.get(key);
  if (!url || result.url !== key) return result;
  // pig divergence (D93): an import of an extension module that a reload evaluated again resolves to that evaluation, from every importer, the extension's entry included.
  return { ...result, url };
}

export function resolve(specifier, context, defaultResolve) {
  const shim = shims.get(specifier);
  if (shim) {
    cacheImportedModules();
    return { url: shim, shortCircuit: true };
  }
  const result = defaultResolve(specifier, context, defaultResolve);
  if (typeof result?.then === "function") return result.then((resolved) => track(resolved, context.parentURL));
  return track(result, context.parentURL);
}

export function load(url, context, nextLoad) {
  if (!extensionModule(url)) return nextLoad(url, context);
  // The stamp is taken before Node reads the source, so an edit made while it reads counts as an edit.
  const stamp = stampOf(url);
  const remember = (loaded) => {
    loads.set(moduleKey(url), { url, format: loaded?.format, stamp });
    return loaded;
  };
  const result = nextLoad(url, context);
  return typeof result?.then === "function" ? result.then(remember) : remember(result);
}

// pig divergence (D93): when a reload pass starts, each extension ES module whose source changed since Node loaded it, and each extension ES module that imports one, directly or through others, gets a URL carrying the pass, so the reload evaluates them again; every other module keeps its instance and its state. A CommonJS module keeps its module, so an edit of one, or of a module only it imports, is not evaluated again. The asynchronous hooks of a Node release without module.registerHooks run in their own thread, so there the maps stay empty and every module keeps its instance, as in Pi.
export function reevaluateEdited(pass) {
  const stale = new Set();
  for (const [key, loaded] of loads) {
    if (reevaluable.has(loaded.format) && stampOf(key) !== loaded.stamp) stale.add(key);
  }
  if (stale.size === 0) return;
  const importers = new Map();
  for (const [key, loaded] of loads) {
    if (!reevaluable.has(loaded.format)) continue;
    for (const child of imports.get(loaded.url) ?? []) {
      if (!importers.has(child)) importers.set(child, new Set());
      importers.get(child).add(key);
    }
  }
  const queue = [...stale];
  while (queue.length > 0) {
    for (const importer of importers.get(queue.shift()) ?? []) {
      if (stale.has(importer)) continue;
      stale.add(importer);
      queue.push(importer);
    }
  }
  for (const key of stale) {
    const url = new URL(key);
    url.searchParams.set(reloadParam, pass);
    latest.set(key, url.href);
  }
}
