import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { shims } from "./loader.mjs";
import { cacheImportedModules } from "./compile-cache.mjs";
import { compiledFactory, runtimeRequire, runtimeSource } from "./runtime-modules.mjs";
import { getRuntimeCacheDir } from "./shims/pig-config.mjs";

// Ports packages/coding-agent/src/core/extensions/loader.ts: loadExtensionModule.
// The published Node distribution uses jiti's Babel transform, virtual modules,
// disabled module caching, and disabled native imports for each extension.
const require = createRequire(import.meta.url);
// jiti is the largest module of the runtime tree and only an extension that is not compiled into the host needs it, so it loads on the first such extension.
let createJitiOnce;
const createJiti = (...args) => (createJitiOnce ??= runtimeRequire("./shims/jiti/lib/jiti.cjs"))(...args);

// Jiti validates each source's contents, not its mtime, before reusing a transform. Separate compiler/runtime versions and environment-controlled transform options as well: jiti's internal cache revision alone is not a release identity.
let compilerIdentityOnce;
const compilerIdentity = () => compilerIdentityOnce ??= createHash("sha256")
  .update(runtimeSource("./jiti-loader.mjs"))
  .update(runtimeRequire("./shims/jiti/package.json").version)
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
  const identity = createHash("sha256").update(compilerIdentity()).update(JSON.stringify(options)).digest("hex");
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

/** Imports an extension's entry the way Pi does and returns its default export. */
export async function importExtension(entry) {
  // A host that compiled this extension in serves its factory, keyed by the entry path the host passes.
  const compiled = compiledFactory(entry);
  if (compiled !== undefined) return compiled;
  const jiti = createJiti(import.meta.url, {
    moduleCache: false,
    fsCache: transformCacheDir(),
    virtualModules,
    tryNative: false,
  });
  return jiti.import(entry, { default: true });
}
