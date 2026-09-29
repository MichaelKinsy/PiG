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

export function resolve(specifier, context, defaultResolve) {
  const shim = shims.get(specifier);
  if (shim) {
    cacheImportedModules();
    return { url: shim, shortCircuit: true };
  }
  return defaultResolve(specifier, context, defaultResolve);
}
