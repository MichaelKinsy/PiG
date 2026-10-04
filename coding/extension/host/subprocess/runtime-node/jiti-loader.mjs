import { createHash } from "node:crypto";
import { readFileSync, realpathSync, statSync } from "node:fs";
import { createRequire } from "node:module";
import { isAbsolute, join } from "node:path";
import { fileURLToPath } from "node:url";
import { moduleKey, nativeImports, reloadParam, shims } from "./loader.mjs";
import { cacheImportedModules } from "./compile-cache.mjs";
import { getRuntimeCacheDir } from "./shims/pig-config.mjs";

// Ports packages/coding-agent/src/core/extensions/loader.ts: loadExtensionModule.
// The published Node distribution uses jiti's Babel transform, virtual modules,
// disabled module caching, and disabled native imports for each extension.
const require = createRequire(import.meta.url);
// jiti's own entry (shims/jiti/lib/jiti.cjs) passes a fixed native import to this constructor; the runtime passes its own, so a reload can evaluate an edited ES module extension again, and otherwise builds jiti as that entry does.
const createJitiCore = require("./shims/jiti/dist/jiti.cjs");
let babelTransform;
function lazyTransform(...args) {
  babelTransform ??= require("./shims/jiti/dist/babel.cjs");
  return babelTransform(...args);
}
function onError(error) {
  throw error;
}

// Jiti validates each source's contents, not its mtime, before reusing a transform. Separate compiler/runtime versions and environment-controlled transform options as well: jiti's internal cache revision alone is not a release identity.
const compilerIdentity = createHash("sha256")
  .update(readFileSync(new URL("./jiti-loader.mjs", import.meta.url)))
  .update(require("./shims/jiti/package.json").version)
  .update(process.version)
  .digest("hex");

// Preserve jiti's explicit cache-disable environment options.
function booleanEnv(name, fallback) {
  try { return name in process.env ? Boolean(JSON.parse(process.env[name])) : fallback; }
  catch { return fallback; }
}
// pig additive (D20): keep source-validated transforms in PiG's disposable cache, separate from immutable cell artifacts.
function transformCacheDir() {
  if (!booleanEnv("JITI_FS_CACHE", booleanEnv("JITI_CACHE", true))) return false;
  const options = Object.entries(process.env).filter(([key]) => key.startsWith("JITI_")).sort(([a], [b]) => a.localeCompare(b));
  const identity = createHash("sha256").update(compilerIdentity).update(JSON.stringify(options)).digest("hex");
  return join(getRuntimeCacheDir(), "jiti", identity);
}

// Jiti reads a virtual module only when an extension imports its specifier. Native require(ESM) returns the same namespace as import(), including across aliases, without eagerly evaluating unrelated SDK/provider module graphs.
const virtualModules = Object.create(null);
for (const [specifier, url] of shims) {
  Object.defineProperty(virtualModules, specifier, {
    enumerable: true,
    get: () => { cacheImportedModules(); return require(fileURLToPath(url)); },
  });
}

// jiti hands an ES module (an .mjs file, or a .js file under "type": "module") to Node's own import when it imports asynchronously, and Node keeps such a module for the life of the process, so Pi keeps running an edited ES module extension's old code after /reload while it evaluates a TypeScript or CommonJS extension's current source.
// pig divergence (D93): the first import of an extension's entry in each reload pass evaluates an ES module extension again when the source of a local module it imported changed since its evaluation; an unedited one keeps its module and its state, as in Pi. evaluations holds, by the entry's canonical path, its module key, the URL of its latest evaluation, when that evaluation started, and the source stamp of each local module it imported.
const evaluations = new Map();

function canonicalPath(pathOrURL) {
  const text = String(pathOrURL);
  let path = text.startsWith("file:") ? fileURLToPath(text) : text;
  try { path = realpathSync.native(path); } catch {}
  return process.platform === "win32" ? path.toLowerCase() : path;
}

function now() {
  return BigInt(Date.now()) * 1_000_000n;
}

function stampOf(key) {
  try {
    const info = statSync(fileURLToPath(key), { bigint: true });
    return { mtime: info.mtimeNs, text: `${info.mtimeNs}:${info.size}` };
  } catch {
    return { mtime: undefined, text: "" };
  }
}

// graphOf lists the local modules that the module with key imports, itself included, directly or through other local modules.
function graphOf(key) {
  const seen = new Set([key]);
  const queue = [key];
  while (queue.length > 0) {
    for (const child of nativeImports.get(queue.shift()) ?? []) {
      if (seen.has(child)) continue;
      seen.add(child);
      queue.push(child);
    }
  }
  return seen;
}

function edited(evaluation) {
  for (const key of graphOf(evaluation.node)) {
    const current = stampOf(key);
    const stamp = evaluation.stamps.get(key);
    if (stamp !== undefined) {
      if (current.text !== stamp) return true;
      continue;
    }
    // A module the extension imported after its factory returned has no stamp: its source changed if its file is newer than the evaluation.
    if (current.mtime === undefined || current.mtime > evaluation.since) return true;
  }
  return false;
}

function nodeOf(path) {
  for (const key of nativeImports.keys()) {
    if (key.startsWith("file:") && canonicalPath(key) === path) return key;
  }
  return undefined;
}

// record keeps an evaluation of an entry that jiti imported natively and stamps the local modules it imported that have no stamp yet. A Node release without module.registerHooks leaves nativeImports empty in this thread, so there nothing is recorded and every import is Pi's.
function record(path, previous, url, since) {
  const node = previous?.node ?? nodeOf(path);
  if (!node) return;
  let evaluation = previous;
  if (!evaluation || evaluation.url !== (url ?? node)) {
    evaluation = { node, url: url ?? node, since, stamps: new Map() };
    evaluations.set(path, evaluation);
  }
  for (const key of graphOf(node)) {
    if (!evaluation.stamps.has(key)) evaluation.stamps.set(key, stampOf(key).text);
  }
}

/** Imports an extension's entry the way Pi does and returns its default export. reload is the reload pass of the admission, or "0" outside a reload. */
export async function importExtension(entry, reload = "0") {
  const path = canonicalPath(entry);
  const previous = evaluations.get(path);
  let url = previous?.url;
  let since = previous?.since ?? now();
  if (previous && reload !== "0" && edited(previous)) {
    const next = new URL(moduleKey(previous.node));
    next.searchParams.set(reloadParam, reload);
    url = next.href;
    since = now();
  }
  let native = false;
  const nativeImport = (id) => {
    const specifier = String(id);
    if ((specifier.startsWith("file:") || isAbsolute(specifier)) && canonicalPath(specifier) === path) {
      native = true;
      if (url) return import(url);
    }
    return import(id);
  };
  const jiti = createJitiCore(import.meta.url, {
    moduleCache: false,
    fsCache: transformCacheDir(),
    virtualModules,
    tryNative: false,
    transform: lazyTransform,
  }, { onError, nativeImport, createRequire });
  const factory = await jiti.import(entry, { default: true });
  if (native) record(path, previous, url, since);
  return factory;
}
