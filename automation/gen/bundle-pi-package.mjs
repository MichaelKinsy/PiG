// Compile a pinned package graph while preserving its external module identities and source/asset URLs.
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, relative, resolve, sep } from "node:path";

const require = createRequire(new URL("../../extensions/sdk-ts/package.json", import.meta.url));
const esbuild = createRequire(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/package.json", import.meta.url))("esbuild");
const ts = require("typescript");

export async function bundleRuntimePackage(shimArg, packageName, entryFiles, { output: outputArg, external = [], json = false } = {}) {
  const shims = resolve(shimArg);
  const source = join(shims, "pi-dist", packageName);
  const logicalOutput = join(source, "sdk-bundle");
  const output = outputArg ? resolve(outputArg) : logicalOutput;
  const externalFiles = new Set(external.map(file => join(source, file)));
  const program = ts.createProgram([...externalFiles], { allowJs: true, target: ts.ScriptTarget.Latest, module: ts.ModuleKind.NodeNext, moduleResolution: ts.ModuleResolutionKind.NodeNext });
  const checker = program.getTypeChecker();
  const externalExports = new Map([...externalFiles].map(path => {
    const file = program.getSourceFile(path);
    const symbol = file && checker.getSymbolAtLocation(file);
    if (!symbol) throw new Error(`Cannot inspect shared exports: ${path}`);
    return [path, checker.getExportsOfModule(symbol).filter(symbol => symbol.name !== "default").map(symbol => symbol.name)];
  }));
  rmSync(output, { recursive: true, force: true });
  mkdirSync(output, { recursive: true });
  const result = await esbuild.build({
    absWorkingDir: shims,
    entryPoints: Object.fromEntries(Object.entries(entryFiles).map(([name, file]) => [name, join(source, file)])),
    outdir: output,
    bundle: true, splitting: true, format: "esm", platform: "node", target: "node24",
    chunkNames: "chunk-[hash]", keepNames: true, metafile: true,
    plugins: [{
      name: "pi-module-boundaries",
      setup(build) {
        build.onResolve({ filter: /.*/ }, args => {
          if (args.kind === "entry-point" || args.path.startsWith("node:")) return undefined;
          if (!args.path.startsWith(".") && !args.path.startsWith("/")) return undefined;
          const path = resolve(args.resolveDir, args.path);
          if (path.startsWith(source + sep) && !externalFiles.has(path) && (path.endsWith(".js") || json && path.endsWith(".json"))) return undefined;
          let specifier = relative(logicalOutput, path).split("\\").join("/");
          if (!specifier.startsWith(".")) specifier = "./" + specifier;
          return { path: specifier, external: true };
        });
        build.onLoad({ filter: /\.js$/ }, args => {
          if (!args.path.startsWith(source + sep)) throw new Error(`Unexpected bundled dependency: ${args.path}`);
          let contents = readFileSync(args.path, "utf8");
          const syntax = ts.createSourceFile(args.path, contents, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
          const edits = [];
          const location = "../" + relative(source, args.path).split("\\").join("/");
          function visit(node) {
            if (ts.isExportDeclaration(node) && !node.exportClause && node.moduleSpecifier && ts.isStringLiteral(node.moduleSpecifier)) {
              const exports = externalExports.get(resolve(dirname(args.path), node.moduleSpecifier.text));
              if (exports) edits.push({ start: node.getStart(syntax), end: node.end, text: `export { ${exports.join(", ")} } from ${JSON.stringify(node.moduleSpecifier.text)};` });
            }
            if (ts.isPropertyAccessExpression(node) && ts.isMetaProperty(node.expression) && node.expression.keywordToken === ts.SyntaxKind.ImportKeyword && node.name.text === "url") {
              edits.push({ start: node.getStart(syntax), end: node.end, text: `new URL(${JSON.stringify(location)}, import.meta.url).href` });
            }
            ts.forEachChild(node, visit);
          }
          visit(syntax);
          for (const edit of edits.sort((a, b) => b.start - a.start)) contents = contents.slice(0, edit.start) + edit.text + contents.slice(edit.end);
          return { contents, loader: "js", resolveDir: dirname(args.path) };
        });
      },
    }],
  });
  const inputs = Object.keys(result.metafile.inputs).sort().map(path => ({ path, sha256: createHash("sha256").update(readFileSync(join(shims, path))).digest("hex") }));
  writeFileSync(join(output, "inputs.json"), JSON.stringify({ esbuild: esbuild.version, inputs }, null, 2) + "\n");
}
