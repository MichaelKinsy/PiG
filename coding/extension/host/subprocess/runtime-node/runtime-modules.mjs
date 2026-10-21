// pig additive (D19): the runtime tree loads some modules on first use, so a process that never needs them does not pay to load them, and it starts its IO worker and reads its own source by URL. A TypeScript runtime that bundles this tree into one executable cannot follow a computed require or URL, so it registers globalThis[Symbol.for("pig.runtimeModules")] before the runtime loads: an object whose require(specifier) returns the module, source(specifier) the file's bytes and worker(specifier) the Worker URL, for each specifier listed below. Without it every specifier resolves in the runtime tree as before. Specifiers are relative to the runtime tree's root, and an unlisted specifier throws in both cases so that this list stays complete. The same host registers globalThis[Symbol.for("pig.compiledFactories")] for the extensions it compiled in (see compiledFactory).
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";

export const RUNTIME_REQUIRES = Object.freeze([
  "./shims/jiti/lib/jiti.cjs",
  "./shims/jiti/package.json",
  "./shims/pi-agent-core.mjs",
  "./shims/pi-ai-models.mjs",
  "./shims/pi-ai-oauth.mjs",
  "./shims/pi-ai.mjs",
  "./shims/pi-coding-agent.mjs",
  "./shims/pi-dist/pi-ai/sdk-bundle/providers.js",
  "./shims/pi-dist/pi-coding-agent/core/provider-composer.js",
  "./shims/pi-dist/pi-coding-agent/core/session-manager.js",
  "./shims/pi-dist/pi-coding-agent/modes/interactive/components/status-indicator.js",
  "./shims/pi-dist/pi-coding-agent/sdk-bundle/index.js",
  "./shims/pi-dist/pi-coding-agent/utils/syntax-highlight.js",
  "./shims/pi-dist/pi-tui/sdk-bundle/index.js",
  "./shims/pi-dist/pi-tui/terminal-image.js",
  "./shims/pi-dist/pi-tui/tui.js",
  "./shims/pi-tui-emoji.mjs",
  "./shims/pi-tui.mjs",
  "./shims/typebox-compile.mjs",
  "./shims/typebox-value.mjs",
  "./shims/typebox.mjs",
]);
export const RUNTIME_SOURCES = Object.freeze(["./jiti-loader.mjs"]);
export const RUNTIME_WORKERS = Object.freeze(["./provider-socket.mjs"]);

const listed = (list, kind, specifier) => {
  if (!list.includes(specifier)) throw new Error(`runtime ${kind} ${specifier} is not listed in runtime-modules.mjs`);
};
// The hook is read at each call: the TypeScript runtime's preload (register-loader.mjs) loads this module before a host registers it.
const runtimeModules = () => globalThis[Symbol.for("pig.runtimeModules")];
const fromTree = createRequire(import.meta.url);

// A host that compiled extensions in registers globalThis[Symbol.for("pig.compiledFactories")], a Map from an extension's entry path to its default export. An entry the Map lacks loads through jiti, or fails where the host did not compile jiti in.
export function compiledFactory(entry) {
  return globalThis[Symbol.for("pig.compiledFactories")]?.get(entry);
}

export function runtimeRequire(specifier) {
  listed(RUNTIME_REQUIRES, "module", specifier);
  const hook = runtimeModules();
  return typeof hook?.require === "function" ? hook.require(specifier) : fromTree(specifier);
}

export function runtimeSource(specifier) {
  listed(RUNTIME_SOURCES, "source", specifier);
  const hook = runtimeModules();
  return typeof hook?.source === "function" ? hook.source(specifier) : readFileSync(new URL(specifier, import.meta.url));
}

export function runtimeWorker(specifier) {
  listed(RUNTIME_WORKERS, "worker", specifier);
  const hook = runtimeModules();
  return typeof hook?.worker === "function" ? hook.worker(specifier) : new URL(specifier, import.meta.url);
}

// moduleRegistry returns the module registry require.cache exposes. On Bun it holds ES modules too, so removing a module from it makes the next import evaluate the module again.
export function moduleRegistry() {
  return fromTree.cache;
}
