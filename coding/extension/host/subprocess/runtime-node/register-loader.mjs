import * as module from "node:module";
import { existsSync, readFileSync } from "node:fs";
import { dirname, extname, isAbsolute, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { extensionModule, load, recordBunImport, recordBunLoad, resolve, shims } from "./loader.mjs";
import { moduleRegistry, runtimeRequire } from "./runtime-modules.mjs";

// Pi's CLI discards process warnings (cli/setup.ts).
process.emitWarning = () => {};

// shimModules loads each module the shims map serves, by the runtime-tree specifier runtime-modules.mjs lists.
const shimModules = new Map([
  ["./shims/pi-agent-core.mjs", () => runtimeRequire("./shims/pi-agent-core.mjs")],
  ["./shims/pi-ai.mjs", () => runtimeRequire("./shims/pi-ai.mjs")],
  ["./shims/pi-ai-models.mjs", () => runtimeRequire("./shims/pi-ai-models.mjs")],
  ["./shims/pi-ai-oauth.mjs", () => runtimeRequire("./shims/pi-ai-oauth.mjs")],
  ["./shims/pi-coding-agent.mjs", () => runtimeRequire("./shims/pi-coding-agent.mjs")],
  ["./shims/pi-dist/pi-ai/sdk-bundle/providers.js", () => runtimeRequire("./shims/pi-dist/pi-ai/sdk-bundle/providers.js")],
  ["./shims/pi-tui.mjs", () => runtimeRequire("./shims/pi-tui.mjs")],
  ["./shims/typebox-compile.mjs", () => runtimeRequire("./shims/typebox-compile.mjs")],
  ["./shims/typebox.mjs", () => runtimeRequire("./shims/typebox.mjs")],
  ["./shims/typebox-value.mjs", () => runtimeRequire("./shims/typebox-value.mjs")],
]);

// Bun runs neither Node hook API, and its runtime plugins do not see a static import that resolves on disk. An installed copy of a package the runtime serves (an extension's node_modules copy of pi-coding-agent, say) would then win over the runtime's module, as it does not under Node. So the plugin answers the load of such a file, when a shimmed specifier resolves to it from that node_modules directory, with the runtime's module.
function serveShimsOnBun() {
  const tree = new URL("./", import.meta.url).href;
  const byPackage = new Map();
  for (const [specifier, url] of shims) {
    const name = specifier.startsWith("@") ? specifier.split("/", 2).join("/") : specifier.split("/", 1)[0];
    const loadShim = shimModules.get(`./${url.slice(tree.length)}`);
    if (!url.startsWith(tree) || !loadShim) throw new Error(`shim ${url} of ${specifier} is not listed in register-loader.mjs`);
    if (!byPackage.has(name)) byPackage.set(name, []);
    byPackage.get(name).push([specifier, loadShim]);
  }
  const marker = "/node_modules/";
  const escape = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&").replace("/", "[\\\\/]");
  // Bun requires an object from every onLoad the filter matches, so the filter matches only the shimmed packages and the plugin reads any other file of theirs itself.
  const filter = new RegExp(`[\\\\/]node_modules[\\\\/](?:${[...byPackage.keys()].map(escape).join("|")})[\\\\/]`);
  const loaders = { ".cjs": "js", ".mjs": "js", ".js": "js", ".json": "json", ".ts": "ts", ".mts": "ts", ".cts": "ts" };
  globalThis.Bun.plugin({
    name: "pig-runtime-shims",
    setup(build) {
      build.onLoad({ filter }, (args) => {
        const path = args.path.replaceAll("\\", "/");
        const at = path.lastIndexOf(marker);
        const rest = path.slice(at + marker.length).split("/");
        const dir = args.path.slice(0, at + 1);
        for (const [specifier, shim] of byPackage.get(rest[0].startsWith("@") ? `${rest[0]}/${rest[1]}` : rest[0]) ?? []) {
          let target;
          try {
            target = globalThis.Bun.resolveSync(specifier, dir);
          } catch {
            continue;
          }
          if (target === args.path) return { exports: shim(), loader: "object" };
        }
        return { contents: readFileSync(args.path, "utf8"), loader: loaders[extname(args.path)] ?? "js" };
      });
    },
  });
}

// packageType returns the "type" of the package.json nearest to dir, as Node reads it to choose a .js or .ts file's module format.
const packageTypes = new Map();
function packageType(dir) {
  if (packageTypes.has(dir)) return packageTypes.get(dir);
  let type;
  try {
    const field = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"))?.type;
    type = typeof field === "string" ? field : "none";
  } catch (error) {
    const parent = dirname(dir);
    type = error?.code === "ENOENT" && parent !== dir ? packageType(parent) : "none";
  }
  packageTypes.set(dir, type);
  return type;
}

// cjsWrapper is Node's CommonJS wrapper parameter list: a source that compiles inside it is CommonJS to Node's module-syntax detection.
const cjsWrapper = ["exports", "require", "module", "__filename", "__dirname"];

// nodeFormat returns the module format Node gives a local file: by extension, then by the nearest package.json "type", then by Node's detection of module syntax in a file with no type. Node requires any other extension as CommonJS.
function nodeFormat(path) {
  const ext = extname(path);
  if (ext === ".mjs") return "module";
  if (ext === ".cjs") return "commonjs";
  if (ext === ".mts") return "module-typescript";
  if (ext === ".cts") return "commonjs-typescript";
  if (ext === ".json") return "json";
  if (ext === ".wasm") return "wasm";
  if (ext !== ".js" && ext !== ".ts") return "commonjs";
  const typescript = ext === ".ts";
  const type = packageType(dirname(path));
  if (type === "module") return typescript ? "module-typescript" : "module";
  if (type === "commonjs") return typescript ? "commonjs-typescript" : "commonjs";
  try {
    const source = readFileSync(path, "utf8");
    new Function(...cjsWrapper, typescript ? new globalThis.Bun.Transpiler({ loader: "ts" }).transformSync(source) : source);
    return typescript ? "commonjs-typescript" : "commonjs";
  } catch {
    return typescript ? "module-typescript" : "module";
  }
}

// nodeImportPath returns the file Node imports for an absolute POSIX path used as a specifier: Node reads it as a URL path, so it decodes percent escapes, drops tabs and line breaks, and treats "?" and "#" as the query and the fragment. Bun opens the path as written.
function nodeImportPath(specifier) {
  if (process.platform === "win32" || !specifier.startsWith("/") || !/[%\t\n\r\\]/.test(specifier)) return undefined;
  try {
    const path = fileURLToPath(new URL(specifier, "file:///"));
    return path === specifier.replace(/[?#].*$/, "") ? undefined : path;
  } catch {
    return undefined;
  }
}

// filePath returns the path a file: URL specifier names, and any other specifier unchanged.
function filePath(specifier) {
  if (!specifier.startsWith("file:")) return specifier;
  try {
    return fileURLToPath(specifier);
  } catch {
    return specifier;
  }
}

// pig divergence (D93): Bun runs no module hook, so this plugin records what loader.mjs's Node hooks record: the extension modules each evaluation imports, and each local extension module's source stamp and module format when Bun is about to load it. Bun resolves a module before it reads it, so the stamp is taken at the first resolution of each evaluation, before Bun reads the source, as load() takes it on Node. The plugin only observes: Bun resolves and loads every module itself.
function trackExtensionModulesOnBun() {
  const registry = moduleRegistry();
  const treeDir = dirname(fileURLToPath(import.meta.url)) + "/";
  // Bun.resolveSync runs this plugin's resolver again; the inner call only resolves.
  let resolving = false;
  globalThis.Bun.plugin({
    name: "pig-extension-modules",
    setup(build) {
      build.onResolve({ filter: /.*/ }, (args) => {
        if (resolving) return undefined;
        const nodePath = nodeImportPath(args.path);
        // Bun's resolver does not say whether require() or import asks, and Node's require() opens the path as written. So the runtime tree's own imports (jiti's import of an extension's entry) and Bun's later resolutions without an importer read the path as Node's import does, and an extension's own specifier does so only when no file has the path as written.
        if (nodePath !== undefined && (!args.importer || args.importer.startsWith(treeDir) || !existsSync(args.path))) return { path: nodePath };
        const importer = args.importer && isAbsolute(args.importer) && extensionModule(pathToFileURL(args.importer).href) ? args.importer : undefined;
        const specifier = filePath(args.path.replace(/[?#].*$/, ""));
        // Only an extension module's imports and an import by absolute path, such as jiti's import of an extension's entry, can load an extension module; the runtime tree's own imports skip the resolution.
        if (!importer && !isAbsolute(specifier)) return undefined;
        let target;
        resolving = true;
        try {
          target = globalThis.Bun.resolveSync(specifier, importer ? dirname(importer) : process.cwd());
        } catch {
          // A specifier that does not resolve fails in Bun's own resolution and loads no module.
          return undefined;
        } finally {
          resolving = false;
        }
        if (!isAbsolute(target)) return undefined;
        const url = pathToFileURL(target).href;
        if (!extensionModule(url)) return undefined;
        if (importer) recordBunImport(pathToFileURL(importer).href, url);
        recordBunLoad(url, target in registry, () => nodeFormat(target));
        return undefined;
      });
    },
  });
}

// Synchronous hooks also cover native require(ESM). Node 22.13 predates registerHooks and needs the asynchronous hook registration API.
if (typeof globalThis.Bun !== "undefined") {
  serveShimsOnBun();
  trackExtensionModulesOnBun();
} else if (typeof module.registerHooks === "function") {
  module.registerHooks({ resolve, load });
} else {
  module.register(new URL("./loader.mjs", import.meta.url));
}
