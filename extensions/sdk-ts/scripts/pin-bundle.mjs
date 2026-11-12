// pi-pin.d.ts: the pinned Pi build's declarations as one script of ambient modules, which index.d.ts references. An ambient module is
// looked up before the installed package and replaces it whole, so a program that imports this package type-checks Pi's packages
// against the pin while the peer dependency stays the published release.
//
//   node scripts/pin-bundle.mjs    write pi-pin.d.ts from the installed pinned build, and the peerDependencies of package.json it needs
//                                  with package-lock.json when they change (make vendor-sdk-ts-pin-declarations)
//
// The bundle has one block per specifier the packages expose and one per further declaration file those reach. A block is its file's
// text with the `declare` modifiers of top-level statements and the source map comment removed and every specifier of a file of the four
// packages replaced by that file's block name. A JSON file a declaration file imports for its type (a model catalog) is a block that
// declares that type. Imports of other packages stay as written and resolve from this package's directory, so package.json declares each
// such package as a peer dependency at the range the importing Pi package declares.
import { spawnSync } from "node:child_process";
import { builtinModules, createRequire } from "node:module";
import { readFileSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { blockNames, compare, describe, moduleSpecifiers, PACKAGES, SCOPE, specifiers, splice } from "./declarations.mjs";
import { pinnedBuild, publishedTree, sdk } from "./trees.mjs";

const require = createRequire(import.meta.url);
const ts = require("typescript");

export const TARGET = join(sdk, "pi-pin.d.ts");

function relativeTarget(tree, file, specifier) {
  const parts = file.split("/").slice(0, -1);
  for (const part of specifier.split("/")) {
    if (part === "..") parts.pop();
    else if (part !== ".") parts.push(part);
  }
  const path = parts.join("/");
  const stem = path.replace(/\.[jt]s$/, "");
  const candidates = path.endsWith(".json") ? [path] : stem === path ? [`${path}.d.ts`, `${path}/index.d.ts`] : [`${stem}.d.ts`];
  return candidates.find((candidate) => tree.has(candidate));
}

// jsonBodies returns, for each JSON file (path -> text), the block that stands for it: a default export of the type the compiler gives
// the JSON module under `strict`, one member a line without indentation.
export function jsonBodies(json) {
  const paths = [...json.keys()].sort();
  if (paths.length === 0) return new Map();
  const files = new Map(paths.map((path, at) => [`/json/${at}.json`, json.get(path)]));
  files.set("/json/use.ts", paths.map((_, at) => `import value${at} from "./${at}.json";\nexport const used${at} = value${at};\n`).join(""));
  const options = { module: ts.ModuleKind.ESNext, moduleResolution: ts.ModuleResolutionKind.Bundler, resolveJsonModule: true, strict: true, noEmit: true, target: ts.ScriptTarget.ES2022, types: [] };
  const host = ts.createCompilerHost(options);
  const { directoryExists, fileExists, readFile, getSourceFile } = host;
  host.directoryExists = (name) => name === "/json" || directoryExists(name);
  host.fileExists = (name) => files.has(name) || fileExists(name);
  host.readFile = (name) => files.get(name) ?? readFile(name);
  host.getSourceFile = (name, target, ...rest) => (files.has(name) ? ts.createSourceFile(name, files.get(name), target, true) : getSourceFile(name, target, ...rest));
  const program = ts.createProgram(["/json/use.ts"], options, host);
  const checker = program.getTypeChecker();
  const source = program.getSourceFile("/json/use.ts");
  const printer = ts.createPrinter({ newLine: ts.NewLineKind.LineFeed });
  const bodies = new Map();
  for (const statement of source.statements) {
    if (!ts.isVariableStatement(statement)) continue;
    const name = statement.declarationList.declarations[0].name;
    const node = checker.typeToTypeNode(checker.getTypeAtLocation(name), undefined, ts.NodeBuilderFlags.NoTruncation | ts.NodeBuilderFlags.MultilineObjectLiterals);
    const type = printer.printNode(ts.EmitHint.Unspecified, node, source).replace(/^ +/gm, "");
    bodies.set(paths[Number(name.text.slice("used".length))], `const value: ${type};\nexport default value;`);
  }
  return bodies;
}

// bundle returns { text, external }: the bundle of a tree under a header, and each specifier left as an import of another package with
// the packages of the tree that import it.
export function bundle(tree, header) {
  const names = blockNames(tree);
  const files = new Map();
  for (const [file, name] of names) {
    if (files.has(name)) throw new Error(`${files.get(name)} and ${file} would both be the block ${name}`);
    files.set(name, file);
  }
  const ours = (specifier) => PACKAGES.some((name) => specifier === SCOPE + name || specifier.startsWith(`${SCOPE}${name}/`));
  const external = new Map();
  const bodies = new Map();
  const json = new Map();
  const todo = [...new Set(specifiers(tree).values())];
  while (todo.length > 0) {
    const file = todo.pop();
    if (bodies.has(file)) continue;
    const text = tree.get(file);
    if (text === undefined) throw new Error(`${file} is exposed or imported and is not in the build`);
    const source = ts.createSourceFile(file, text, ts.ScriptTarget.ES2022, true);
    if (!ts.isExternalModule(source)) throw new Error(`${file} is a script: its declarations are global and no module block can carry them`);
    if (source.referencedFiles.length + source.typeReferenceDirectives.length + source.libReferenceDirectives.length > 0) throw new Error(`${file} has a reference directive, which a module block cannot carry`);
    const edits = [];
    for (const literal of moduleSpecifiers(source)) {
      const specifier = literal.text;
      if (!specifier.startsWith(".") && !ours(specifier)) {
        external.set(specifier, (external.get(specifier) ?? new Set()).add(file.split("/").slice(0, 2).join("/")));
        continue;
      }
      const target = specifier.startsWith(".") ? relativeTarget(tree, file, specifier) : files.get(specifier);
      if (!target) throw new Error(`${file} imports ${specifier}, which names no declaration file ${specifier.startsWith(".") ? "of the build" : "the packages expose"}`);
      if (target.endsWith(".json")) {
        const clause = ts.isImportDeclaration(literal.parent) ? literal.parent.importClause : undefined;
        if (!clause?.name || clause.namedBindings) throw new Error(`${file} imports ${specifier} other than as a default import, which the bundle does not carry`);
        json.set(target, tree.get(target));
      } else todo.push(target);
      edits.push([literal.getStart(source), literal.end, JSON.stringify(names.get(target))]);
    }
    for (const statement of source.statements) {
      if (ts.isExportAssignment(statement) && statement.isExportEquals) throw new Error(`${file} has an export assignment (export =), which the bundle does not carry`);
      if (ts.isModuleDeclaration(statement) && ts.isStringLiteral(statement.name) && (statement.name.text.startsWith(".") || (ours(statement.name.text) && !files.has(statement.name.text)))) {
        throw new Error(`${file} augments ${statement.name.text}, which is no block of the bundle`);
      }
      const modifier = ts.canHaveModifiers(statement) ? ts.getModifiers(statement)?.find((found) => found.kind === ts.SyntaxKind.DeclareKeyword) : undefined;
      if (modifier) edits.push([modifier.getStart(source), modifier.end + (text[modifier.end] === " " ? 1 : 0), ""]);
    }
    bodies.set(file, splice(text, edits).replace(/\n\/\/# sourceMappingURL=\S*\s*$/, "\n").trimEnd());
  }
  for (const [file, body] of jsonBodies(json)) bodies.set(file, body);
  const order = [...bodies.keys()].sort((a, b) => (names.get(a) < names.get(b) ? -1 : 1));
  const text = `${header}\n${order.map((file) => `declare module ${JSON.stringify(names.get(file))} {\n${bodies.get(file)}\n}\n`).join("")}`;
  return { text, external: new Map([...external].sort(([a], [b]) => (a < b ? -1 : 1)).map(([specifier, packages]) => [specifier, [...packages].sort()])) };
}

const PI = `${SCOPE}pi-coding-agent`;

// peers returns the peer dependencies the bundle needs, name -> range: each package it imports from outside the four, at the range the
// Pi package that imports it declares. `packages` maps a Pi package to its { manifest }. Node's own modules need no package.
export function peers(external, packages) {
  const wanted = new Map();
  for (const [specifier, importers] of external) {
    if (specifier.startsWith("node:") || builtinModules.includes(specifier)) continue;
    const name = specifier.split("/").slice(0, specifier.startsWith("@") ? 2 : 1).join("/");
    for (const importer of importers) {
      const { manifest } = packages.get(importer);
      const range = manifest.dependencies?.[name] ?? manifest.peerDependencies?.[name];
      if (!range) throw new Error(`${importer} imports ${specifier} and declares no dependency on ${name}`);
      if (wanted.has(name) && wanted.get(name) !== range) throw new Error(`the Pi packages that import ${name} declare it as ${wanted.get(name)} and as ${range}`);
      wanted.set(name, range);
    }
  }
  return new Map([...wanted].sort(([a], [b]) => (a < b ? -1 : 1)));
}

// peerProblems lists how a manifest's peerDependencies differ from the peers the bundle needs beside Pi itself.
export function peerProblems(declared = {}, wanted) {
  const problems = [];
  for (const [name, range] of wanted) {
    if (!(name in declared)) problems.push(`the bundle imports ${name}, which is no peer dependency`);
    else if (declared[name] !== range) problems.push(`the peer dependency ${name} is ${declared[name]}; Pi declares ${range}`);
  }
  for (const name of Object.keys(declared)) {
    if (name !== PI && !wanted.has(name)) problems.push(`${name} is a peer dependency the bundle does not import`);
  }
  return problems;
}

// header is the bundle's leading comment: what was bundled, what differs from the published release, and Pi's licence.
export function header({ version, commit, release, rows, licence }) {
  const differences = rows.public.length > 0 ? rows.public.map(describe) : ["none"];
  return [
    "// Code generated by scripts/pin-bundle.mjs (make vendor-sdk-ts-pin-declarations). DO NOT EDIT.",
    "//",
    `// The declarations of Pi ${version} at commit ${commit}, the build PiG is pinned to, as ambient modules: one per specifier of`,
    `// ${PACKAGES.map((name) => SCOPE + name).join(", ")},`,
    '// and one under a private "pi-pin:" specifier per further declaration file. index.d.ts references this file, so a program that',
    "// imports @michaelkinsy/pig-extension-types type-checks these packages against the pin, not against the installed release.",
    "//",
    `// Exported declarations that differ from the published Pi ${release} (PiG has the pin's; a Pi ${release} runtime does not):`,
    ...differences.map((line) => `//   ${line}`),
    "//",
    "// Derived from Pi (https://github.com/earendil-works/pi) under its licence:",
    "//",
    ...licence.trimEnd().split("\n").map((line) => (line ? `// ${line}` : "//")),
    "",
  ].join("\n");
}

// generate returns the bundle of the installed pinned build: { text, external, rows, peers }, rows being its differences from the
// published release and peers the peer dependencies it needs.
export function generate() {
  const { tree, commit, packages } = pinnedBuild();
  const release = publishedTree();
  const rows = compare(release.tree, tree);
  const version = JSON.parse(tree.get(`${SCOPE}pi-coding-agent/package.json`)).version;
  const licence = readFileSync(resolve(sdk, "../../third_party/pi-packs/LICENSE"), "utf8");
  const bundled = bundle(tree, header({ version, commit, release: release.version, rows, licence }));
  return { ...bundled, rows, peers: peers(bundled.external, packages) };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const { text, rows, peers: wanted } = generate();
  writeFileSync(TARGET, text);
  console.log(`${TARGET}: ${text.length} bytes, ${rows.public.length} exported differences from the published release`);
  const path = join(sdk, "package.json");
  const manifest = JSON.parse(readFileSync(path, "utf8"));
  if (peerProblems(manifest.peerDependencies, wanted).length > 0) {
    manifest.peerDependencies = Object.fromEntries([[PI, manifest.peerDependencies[PI]], ...wanted].sort(([a], [b]) => (a < b ? -1 : 1)));
    writeFileSync(path, `${JSON.stringify(manifest, null, 2)}\n`);
    console.log(`${path}: peerDependencies ${[...wanted].map(([name, range]) => `${name}@${range}`).join(" ")}`);
  }
  const lock = JSON.parse(readFileSync(join(sdk, "package-lock.json"), "utf8"));
  const sorted = (dependencies = {}) => JSON.stringify(Object.entries(dependencies).sort());
  if (sorted(lock.packages[""].peerDependencies) !== sorted(manifest.peerDependencies)) {
    console.log(`${join(sdk, "package-lock.json")}: recording the peerDependencies (npm install --package-lock-only)`);
    const locked = spawnSync("npm", ["install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund"], { cwd: sdk, stdio: "inherit", shell: process.platform === "win32" });
    if (locked.status !== 0) process.exit(locked.status ?? 1);
  }
}
