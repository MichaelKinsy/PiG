// Declaration drift between two builds of Pi's TypeScript packages, and the bundle of one build's declarations read back as a build.
//
// A tree is a Map from "@earendil-works/<package>/<path>" to text: each package's package.json (its name, types and exports) and every
// declaration file under its dist/ (the pinned build's tree also has the JSON files there, which the comparison does not read). Both trees are laid out as one node_modules in memory and read through the TypeScript checker, so the
// published build and the pinned source build are compared the same way, whatever the directories they came from.
//
// A declaration is a top-level statement, compared as printed without comments. The public surface is what an extension author can name:
// every symbol exported by an entry point of a package's exports map (its `types` file when the package has no map), and every top-level
// declaration those declarations mention, transitively. `compare` reports a difference in a declaration of the public surface apart
// from a difference elsewhere in the declaration files.
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const ts = require("typescript");

const SCOPE = "@earendil-works/";
const ROOT = "/virtual/node_modules/";
const PACKAGES = ["pi-coding-agent", "pi-ai", "pi-tui", "pi-agent-core"];
const PRIVATE = "pi-pin:";

export { PACKAGES, PRIVATE, SCOPE };

// exposed maps each specifier of a package an author can import to the declaration file it names: the `types` targets of the exports
// map, a `*` pattern once per file it matches, or the `types` file of a package without a map.
function exposed(manifest, files, name) {
  const found = new Map();
  const add = (subpath, target) => {
    if (typeof target !== "string" || !target.endsWith(".d.ts")) return;
    const specifier = name + subpath.slice(1);
    const path = target.replace(/^\.\//, "");
    if (!path.includes("*")) {
      found.set(specifier, `${name}/${path}`);
      return;
    }
    const [head, tail] = path.split("*");
    for (const file of files) {
      if (file.startsWith(`${name}/${head}`) && file.endsWith(tail)) found.set(specifier.replace("*", file.slice(name.length + 1 + head.length, file.length - tail.length)), file);
    }
  };
  const walk = (subpath, value) => {
    if (typeof value !== "object") return;
    for (const [key, target] of Object.entries(value ?? {})) {
      if (key === "types") add(subpath, target);
      else walk(key.startsWith(".") ? key : subpath, target);
    }
  };
  if (manifest.exports) walk(".", manifest.exports);
  else add(".", manifest.types);
  return found;
}

// specifiers maps every specifier a tree's packages expose to its declaration file, in specifier order.
export function specifiers(tree) {
  const files = [...tree.keys()];
  const found = [];
  for (const name of new Set(files.map((path) => path.split("/").slice(0, 2).join("/")))) {
    found.push(...exposed(JSON.parse(tree.get(`${name}/package.json`)), files, name));
  }
  return new Map(found.sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
}

function program(tree) {
  const files = new Map();
  for (const [path, text] of tree) files.set(ROOT + path, text);
  const directories = new Set();
  for (const path of files.keys()) {
    for (let at = path.lastIndexOf("/"); at > 0; at = path.lastIndexOf("/", at - 1)) directories.add(path.slice(0, at));
  }
  const options = { module: ts.ModuleKind.Node16, moduleResolution: ts.ModuleResolutionKind.Node16, target: ts.ScriptTarget.ES2022, noEmit: true, skipLibCheck: true, types: [], noLib: true };
  const host = {
    getSourceFile: (name, target) => (files.has(name) ? ts.createSourceFile(name, files.get(name), target, true) : undefined),
    getDefaultLibFileName: () => "/virtual/lib.d.ts",
    writeFile() {},
    getCurrentDirectory: () => "/virtual",
    getDirectories: () => [],
    getCanonicalFileName: (name) => name,
    useCaseSensitiveFileNames: () => true,
    getNewLine: () => "\n",
    fileExists: (name) => files.has(name),
    readFile: (name) => files.get(name),
    directoryExists: (name) => directories.has(name),
    realpath: (name) => name,
  };
  const roots = [...new Set(specifiers(tree).values())].sort().map((path) => ROOT + path);
  return { program: ts.createProgram(roots, options, host), roots, files, options, host };
}

// moduleSpecifiers lists the string literals under a node that name a module: those of imports, re-exports and import types.
export function moduleSpecifiers(node, found = []) {
  if (ts.isStringLiteral(node)) {
    const parent = node.parent;
    const named = (ts.isImportDeclaration(parent) || ts.isExportDeclaration(parent)) && parent.moduleSpecifier === node;
    const typed = ts.isLiteralTypeNode(parent) && ts.isImportTypeNode(parent.parent) && parent.parent.argument === parent;
    if (named || typed || ts.isExternalModuleReference(parent)) found.push(node);
  }
  ts.forEachChild(node, (child) => {
    moduleSpecifiers(child, found);
  });
  return found;
}

// splice applies [start, end, replacement] edits to a text.
export function splice(text, edits) {
  let result = text;
  for (const [start, end, replacement] of [...edits].sort((a, b) => b[0] - a[0])) result = result.slice(0, start) + replacement + result.slice(end);
  return result;
}

// relativeSpecifier is the specifier by which the declaration file `from` imports the declaration file `to` (tree paths).
function relativeSpecifier(from, to) {
  const here = from.split("/").slice(0, -1);
  const there = to.replace(/\.d\.ts$/, ".js").split("/");
  let same = 0;
  while (same < here.length && same < there.length - 1 && here[same] === there[same]) same++;
  return [...(same === here.length ? ["."] : here.slice(same).map(() => "..")), ...there.slice(same)].join("/");
}

// normalized returns the declaration files a tree's entry points reach in the form a tree and its bundle are compared in: every module
// specifier that resolves to a file of the tree is the relative path to that file, and no top-level statement has the `declare`
// modifier (a declaration file implies it, and a block of an ambient module forbids it).
export function normalized(tree) {
  const built = program(tree);
  const result = new Map();
  for (const [path, text] of tree) if (path.endsWith("/package.json")) result.set(path, text);
  for (const source of built.program.getSourceFiles()) {
    if (!built.files.has(source.fileName)) continue;
    const file = source.fileName.slice(ROOT.length);
    const edits = [];
    for (const literal of moduleSpecifiers(source)) {
      const mode = ts.getModeForUsageLocation(source, literal, built.options);
      const resolved = ts.resolveModuleName(literal.text, source.fileName, built.options, built.host, undefined, undefined, mode).resolvedModule?.resolvedFileName;
      if (resolved && built.files.has(resolved)) edits.push([literal.getStart(source), literal.end, JSON.stringify(relativeSpecifier(file, resolved.slice(ROOT.length)))]);
    }
    for (const statement of source.statements) {
      const modifier = ts.canHaveModifiers(statement) ? ts.getModifiers(statement)?.find((found) => found.kind === ts.SyntaxKind.DeclareKeyword) : undefined;
      if (modifier) edits.push([modifier.getStart(source), modifier.end, ""]);
    }
    result.set(file, splice(source.text, edits));
  }
  return result;
}

// blockNames maps every declaration file of a tree to the name of its block in the bundle: the specifier that exposes it, its path as
// an author imports it when its package has no exports map, else a private specifier, which no package name can equal. A JSON file has
// a private specifier too.
export function blockNames(tree) {
  const exposing = new Map();
  for (const [specifier, file] of specifiers(tree)) {
    if (exposing.has(file)) throw new Error(`${exposing.get(file)} and ${specifier} expose one file, ${file}`);
    exposing.set(file, specifier);
  }
  const names = new Map();
  for (const file of [...tree.keys()].sort()) {
    if (file.endsWith(".json") && !file.endsWith("/package.json")) names.set(file, PRIVATE + file);
    if (!file.endsWith(".d.ts")) continue;
    const mapped = JSON.parse(tree.get(`${file.split("/").slice(0, 2).join("/")}/package.json`)).exports !== undefined;
    names.set(file, exposing.get(file) ?? (mapped ? PRIVATE + file.replace(/\.d\.ts$/, "") : file.replace(/\.d\.ts$/, ".js")));
  }
  return names;
}

function blocks(text) {
  const source = ts.createSourceFile("/bundle.d.ts", text, ts.ScriptTarget.ES2022, true);
  for (const statement of source.statements) {
    if (!ts.isModuleDeclaration(statement) || !ts.isStringLiteral(statement.name) || !statement.body || !ts.isModuleBlock(statement.body)) {
      throw new Error(`the bundle has a statement that is no module block: ${statement.getText(source).slice(0, 80)}`);
    }
  }
  return { source, statements: source.statements };
}

// blockSpecifiers lists the module names a bundle declares.
export function blockSpecifiers(text) {
  return blocks(text).statements.map((statement) => statement.name.text);
}

// unbundle reads a bundle back as a tree: each block is the declaration file its name stands for among the files of `tree`, beside the
// manifests of `tree`, and a private specifier is the relative path to its file.
export function unbundle(text, tree) {
  const { source, statements } = blocks(text);
  const files = new Map([...blockNames(tree)].map(([file, name]) => [name, file]));
  const result = new Map();
  for (const [path, manifest] of tree) if (path.endsWith("/package.json")) result.set(path, manifest);
  for (const statement of statements) {
    const file = files.get(statement.name.text);
    if (!file) throw new Error(`the bundle's block ${statement.name.text} stands for no declaration file of the build`);
    if (result.has(file)) throw new Error(`the bundle has two blocks ${statement.name.text}`);
    const start = statement.body.statements.pos;
    const edits = [];
    for (const literal of moduleSpecifiers(statement.body)) {
      if (!literal.text.startsWith(PRIVATE)) continue;
      const target = files.get(literal.text);
      if (!target) throw new Error(`${statement.name.text} imports ${literal.text}, which stands for no declaration file of the build`);
      edits.push([literal.getStart(source) - start, literal.end - start, JSON.stringify(relativeSpecifier(file, target))]);
    }
    result.set(file, splice(text.slice(start, statement.body.statements.end), edits));
  }
  return result;
}

const printer = ts.createPrinter({ removeComments: true, newLine: ts.NewLineKind.LineFeed });

function print(node, sourceFile) {
  return printer.printNode(ts.EmitHint.Unspecified, node, sourceFile);
}

function isPrivate(member) {
  return (ts.getCombinedModifierFlags(member) & ts.ModifierFlags.Private) !== 0 || (member.name && ts.isPrivateIdentifier(member.name));
}

function memberKind(member) {
  const kind = ts.SyntaxKind[member.kind].replace(/Declaration$|Signature$/, "").toLowerCase();
  return isPrivate(member) ? `private ${kind}` : kind;
}

function memberName(member, sourceFile) {
  if (member.name) return member.name.getText(sourceFile);
  return ts.SyntaxKind[member.kind];
}

function statementNames(statement, sourceFile) {
  if (ts.isVariableStatement(statement)) return statement.declarationList.declarations.map((d) => d.name.getText(sourceFile)).join(",");
  if (ts.isExportDeclaration(statement)) return `export ${statement.exportClause ? print(statement.exportClause, sourceFile) : "*"} from ${statement.moduleSpecifier?.getText(sourceFile) ?? ""}`;
  if (ts.isExportAssignment(statement)) return "export=";
  if (ts.isImportDeclaration(statement)) return `import ${statement.moduleSpecifier.getText(sourceFile)}`;
  return statement.name ? statement.name.getText(sourceFile) : ts.SyntaxKind[statement.kind];
}

function statementKind(statement) {
  if (ts.isVariableStatement(statement)) return statement.declarationList.flags & ts.NodeFlags.Const ? "const" : "variable";
  return ts.SyntaxKind[statement.kind].replace(/Declaration$/, "").replace(/^TypeAlias$/, "type").toLowerCase();
}

// units reads every top-level declaration of every declaration file: key "<package>/<path>#<name>" -> { kind, texts, members, header }.
function units(built) {
  const result = new Map();
  for (const [name, text] of built.files) {
    if (!name.endsWith(".d.ts")) continue;
    const source = ts.createSourceFile(name, text, ts.ScriptTarget.ES2022, true);
    const file = source.fileName.slice(ROOT.length);
    for (const statement of source.statements) {
      if (ts.isImportDeclaration(statement)) continue;
      const key = `${file}#${statementNames(statement, source)}`;
      let unit = result.get(key);
      if (!unit) {
        unit = { kind: statementKind(statement), texts: [], members: new Map(), headers: [] };
        result.set(key, unit);
      }
      unit.texts.push(print(statement, source));
      if (ts.isClassDeclaration(statement) || ts.isInterfaceDeclaration(statement)) {
        const empty = ts.isClassDeclaration(statement)
          ? ts.factory.updateClassDeclaration(statement, statement.modifiers, statement.name, statement.typeParameters, statement.heritageClauses, [])
          : ts.factory.updateInterfaceDeclaration(statement, statement.modifiers, statement.name, statement.typeParameters, statement.heritageClauses, []);
        unit.headers.push(print(empty, source));
        for (const member of statement.members) {
          const name = memberName(member, source);
          const entry = unit.members.get(name) ?? { kind: memberKind(member), texts: [] };
          entry.texts.push(print(member, source));
          unit.members.set(name, entry);
        }
      }
    }
  }
  return result;
}

// surface lists the keys of the declarations an author can name.
function surface(built) {
  const checker = built.program.getTypeChecker();
  const keys = new Set();
  const seen = new Set();
  const todo = [];
  const resolve = (symbol) => (symbol.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(symbol) : symbol);
  const topLevel = (declaration) => {
    let node = declaration;
    while (node.parent && !ts.isSourceFile(node.parent)) node = node.parent;
    return ts.isSourceFile(node.parent ?? node) ? node : undefined;
  };
  const enqueue = (symbol) => {
    if (!symbol || seen.has(symbol)) return;
    seen.add(symbol);
    todo.push(symbol);
  };
  for (const root of built.roots) {
    const source = built.program.getSourceFile(root);
    const module = source && checker.getSymbolAtLocation(source);
    if (module) for (const exported of checker.getExportsOfModule(module)) enqueue(resolve(exported));
  }
  while (todo.length > 0) {
    const symbol = todo.pop();
    for (const declaration of symbol.declarations ?? []) {
      const source = declaration.getSourceFile();
      if (!built.files.has(source.fileName)) continue;
      const statement = topLevel(declaration);
      if (!statement) continue;
      keys.add(`${source.fileName.slice(ROOT.length)}#${statementNames(statement, source)}`);
      const visit = (node) => {
        if (ts.isIdentifier(node)) {
          const found = checker.getSymbolAtLocation(node);
          if (found) enqueue(resolve(found));
        }
        ts.forEachChild(node, visit);
      };
      visit(statement);
    }
  }
  return keys;
}

function sameTexts(a, b) {
  return a.length === b.length && a.every((text, at) => text === b[at]);
}

// compare returns { public, other }: rows { key, kind, change: "added" | "removed" | "changed", members: [string] } sorted by key.
export function compare(published, pinned) {
  const left = program(published);
  const right = program(pinned);
  const a = units(left);
  const b = units(right);
  const exposed = new Set([...surface(left), ...surface(right)]);
  const rows = { public: [], other: [] };
  for (const key of [...new Set([...a.keys(), ...b.keys()])].sort()) {
    const was = a.get(key);
    const now = b.get(key);
    let row;
    if (!was) row = { key, kind: now.kind, change: "added", members: [] };
    else if (!now) row = { key, kind: was.kind, change: "removed", members: [] };
    else if (!sameTexts(was.texts, now.texts)) {
      const members = [];
      for (const name of [...new Set([...was.members.keys(), ...now.members.keys()])].sort()) {
        const old = was.members.get(name);
        const next = now.members.get(name);
        if (!old) members.push(`member ${name} (${next.kind}) added`);
        else if (!next) members.push(`member ${name} (${old.kind}) removed`);
        else if (!sameTexts(old.texts, next.texts)) members.push(`member ${name} (${next.kind}) changed`);
      }
      if (!sameTexts(was.headers, now.headers)) members.unshift("header changed");
      row = { key, kind: now.kind, change: "changed", members };
    }
    if (row) rows[exposed.has(key) ? "public" : "other"].push(row);
  }
  return rows;
}

export function describe(row) {
  const detail = row.members.length > 0 ? `: ${row.members.join("; ")}` : "";
  return `${row.key} (${row.kind}) ${row.change}${detail}`;
}
