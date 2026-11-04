// The Pi packages other than pi-coding-agent that the runtime serves from a shim module, each given a package root
// laid out like Pi's: shims/pi-dist/<name>/package.json is the pinned package's, and each entry below is a file Pi's
// package.json names (main, an exports target, a bin's unbundled twin) that re-exports the runtime module serving it.
// The vendored dist/ of these packages sits directly under the package root, so the entries are the only dist/ files.
// Keys are the package directory under shims/pi-dist; values map a dist/ path to the file it forwards to,
// relative to that dist/ file. loader.mjs serves each package's specifiers from these files, and the manifest script
// records them as generated.
export const packageRoots = {
  "pi-agent-core": { "dist/index.js": "../../../pi-agent-core.mjs" },
  "pi-tui": { "dist/index.js": "../../../pi-tui.mjs" },
  // The pi-ai root and pi-ai/compat are one module in Pi's extension loader (core/extensions/virtual-modules.ts:28-29), so both resolve to dist/index.js.
  "pi-ai": {
    "dist/index.js": "../../../pi-ai.mjs",
    "dist/models.js": "../../../pi-ai-models.mjs",
    "dist/oauth.js": "../../../pi-ai-oauth.mjs",
    "dist/providers/all.js": "../../sdk-bundle/providers.js",
    "dist/cli.js": "../cli.js",
  },
};
