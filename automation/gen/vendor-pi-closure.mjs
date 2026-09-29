// Copy the module graph of a pinned Pi package into the Node extension
// runtime's shims, starting from one entry file and following every import.
// Called by vendor-pi-dist.sh; run that script, not this one.
//
// Each module is copied verbatim except for the specifiers of bare imports,
// which are rewritten to the vendored copy by relative path so the modules
// load without the extension loader. Relative imports stay as they are. A
// bare specifier with no entry in the map fails the run, so a pin change that
// adds a dependency cannot ship a module that does not load.
//
// usage: node vendor-pi-closure.mjs <shims dir> <spec json>
// spec: { entries: [<file>...], packages: { <package name>: { root, to, vendored } },
//         external: { <specifier>: <file relative to the shims dir> } }
// A package's modules land at <shims>/<to>/<path relative to its dist>. A
// package marked vendored is already copied (with its own rewrites); imports
// of it point at that copy and its modules are not walked.
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { builtinModules, createRequire } from "node:module";
import { dirname, join, relative, resolve, sep } from "node:path";

const [shims, specJSON] = process.argv.slice(2);
const spec = JSON.parse(specJSON);
const packages = Object.entries(spec.packages).map(([name, pkg]) => {
  const manifest = JSON.parse(readFileSync(join(pkg.root, "package.json"), "utf8"));
  return { name, root: pkg.root, dist: join(pkg.root, "dist"), to: pkg.to, vendored: Boolean(pkg.vendored), exports: manifest.exports ?? {} };
});

// The compiler parser includes multiline re-exports and lazy literal imports.
const ts = createRequire(new URL("../../extensions/sdk-ts/package.json", import.meta.url))("typescript");

function exportTarget(pkg, subpath) {
  const conditions = (value) => (typeof value === "string" ? value : value?.import ?? value?.default);
  const direct = pkg.exports[subpath];
  if (direct !== undefined) return conditions(direct);
  for (const [pattern, value] of Object.entries(pkg.exports)) {
    const star = pattern.indexOf("*");
    if (star < 0) continue;
    const prefix = pattern.slice(0, star);
    const suffix = pattern.slice(star + 1);
    if (subpath.startsWith(prefix) && subpath.endsWith(suffix) && subpath.length >= prefix.length + suffix.length) {
      return conditions(value)?.replace("*", subpath.slice(prefix.length, subpath.length - suffix.length));
    }
  }
  return undefined;
}

// Returns the source file a specifier names and where its copy lands, or the
// vendored file an external specifier maps to.
function resolveSpecifier(specifier, from) {
  if (specifier.startsWith("./") || specifier.startsWith("../")) {
    const file = resolve(dirname(from), specifier);
    return { file, pkg: owner(file) };
  }
  for (const pkg of packages) {
    if (specifier !== pkg.name && !specifier.startsWith(pkg.name + "/")) continue;
    if (Object.hasOwn(spec.external, specifier)) return { external: join(shims, spec.external[specifier]) };
    const subpath = "." + specifier.slice(pkg.name.length);
    const target = exportTarget(pkg, subpath);
    if (!target) throw new Error(`${specifier}: no export ${subpath} in ${pkg.name}`);
    const file = resolve(pkg.root, target);
    return { file, pkg };
  }
  if (Object.hasOwn(spec.external, specifier)) return { external: join(shims, spec.external[specifier]) };
  for (const [prefix, target] of Object.entries(spec.externalPrefixes ?? {})) {
    if (specifier.startsWith(prefix)) return { external: join(shims, target, specifier.slice(prefix.length)) };
  }
  return undefined;
}

function owner(file) {
  const pkg = packages.find((candidate) => file.startsWith(candidate.dist + sep));
  if (!pkg) throw new Error(`${file} is outside every vendored package's dist`);
  return pkg;
}

function destination(file) {
  const pkg = owner(file);
  return join(shims, pkg.to, relative(pkg.dist, file));
}

function relativeSpecifier(fromCopy, toCopy) {
  let path = relative(dirname(fromCopy), toCopy).split(sep).join("/");
  if (!path.startsWith(".")) path = "./" + path;
  return path;
}

const seen = new Set();
const queue = spec.entries.map((entry) => resolve(entry));
let copied = 0;
while (queue.length > 0) {
  const file = queue.shift();
  if (seen.has(file)) continue;
  seen.add(file);
  const copy = destination(file);
  let source = readFileSync(file, "utf8");
  const rewrite = (match, head, specifier, tail) => {
    if (specifier.startsWith("node:") || builtinModules.includes(specifier)) return match;
    const resolved = resolveSpecifier(specifier, file);
    const override = resolved?.file && spec.overrides?.[`${owner(resolved.file).name}/${relative(owner(resolved.file).dist, resolved.file).split(sep).join("/")}`];
    if (override) return `${head}"${relativeSpecifier(copy, join(shims, override))}"${tail}`;
    if (!resolved) throw new Error(`${file}: import of "${specifier}" has no vendored copy`);
    if (resolved.external) return `${head}"${relativeSpecifier(copy, resolved.external)}"${tail}`;
    if (!resolved.pkg.vendored) queue.push(resolved.file);
    if (specifier.startsWith(".")) return match;
    return `${head}"${relativeSpecifier(copy, destination(resolved.file))}"${tail}`;
  };
  const syntax = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const edits = [];
  function visit(node) {
    let literal;
    if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier) literal = node.moduleSpecifier;
    else if (ts.isCallExpression(node) && node.expression.kind === ts.SyntaxKind.ImportKeyword && node.arguments.length === 1) literal = node.arguments[0];
    if (literal && ts.isStringLiteral(literal)) {
      const original = literal.getText(syntax);
      const replacement = rewrite(original, "", literal.text, "");
      if (replacement !== original) edits.push({ start: literal.getStart(syntax), end: literal.end, replacement });
    }
    ts.forEachChild(node, visit);
  }
  visit(syntax);
  for (const edit of edits.sort((a, b) => b.start - a.start)) source = source.slice(0, edit.start) + edit.replacement + source.slice(edit.end);
  mkdirSync(dirname(copy), { recursive: true });
  if (source === readFileSync(file, "utf8")) copyFileSync(file, copy);
  else writeFileSync(copy, source);
  copied++;
}
console.log(`vendored ${copied} modules reachable from ${spec.entries.join(", ")}`);
