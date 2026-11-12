// Declaration drift between two builds of Pi's TypeScript packages.
//
// A tree is a Map from "@earendil-works/<package>/<path>" to text: each package's package.json (its name, types and exports) and every
// declaration file under its dist/. Both trees are laid out as one node_modules in memory and read through the TypeScript checker, so the
// published build and the pinned source build are compared the same way, whatever the directories they came from.
//
// A declaration is a top-level statement, compared as printed without comments. The public surface is what an extension author can name:
// every symbol exported by an entry point of a package's exports map (its `types` file when the package has no map), and every top-level
// declaration those declarations mention, transitively. A difference in a declaration of the public surface is a failure; a difference
// elsewhere in the declaration files is reported separately.
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const ts = require("typescript");

const SCOPE = "@earendil-works/";
const ROOT = "/virtual/node_modules/";
const PACKAGES = ["pi-coding-agent", "pi-ai", "pi-tui", "pi-agent-core"];

export { PACKAGES };

function entryPoints(manifest, files, name) {
  const wanted = new Set();
  const add = (target) => {
    if (typeof target !== "string") return;
    const path = target.replace(/^\.\//, "");
    if (!path.includes("*")) {
      wanted.add(`${name}/${path}`);
      return;
    }
    const [head, tail] = path.split("*");
    for (const file of files) {
      if (file.startsWith(`${name}/${head}`) && file.endsWith(tail)) wanted.add(file);
    }
  };
  const walk = (value) => {
    if (typeof value === "string") return;
    for (const [condition, target] of Object.entries(value ?? {})) {
      if (condition === "types") add(target);
      else if (typeof target === "object") walk(target);
    }
  };
  if (manifest.exports) walk(manifest.exports);
  else add(manifest.types);
  return [...wanted].filter((file) => file.endsWith(".d.ts")).sort();
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
  const roots = [];
  const names = new Set([...tree.keys()].map((path) => path.split("/").slice(0, 2).join("/")));
  for (const name of [...names].sort()) {
    const manifest = JSON.parse(tree.get(`${name}/package.json`));
    roots.push(...entryPoints(manifest, tree.keys(), name).map((path) => ROOT + path));
  }
  return { program: ts.createProgram(roots, options, host), roots, files };
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
