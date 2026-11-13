// An extension author's project on disk, laid out as npm installs one, and what the TypeScript compiler says of it.
import { existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { PACKAGES } from "./declarations.mjs";
import { sdk } from "./pinned-commit.mjs";

const require = createRequire(import.meta.url);
const ts = require("typescript");

export const TYPES = "@michaelkinsy/pig-extension-types";

// The module resolutions an author's tsconfig may name, each with and without skipLibCheck.
export const CONFIGURATIONS = [
  { module: ts.ModuleKind.Node16, moduleResolution: ts.ModuleResolutionKind.Node16 },
  { module: ts.ModuleKind.Preserve, moduleResolution: ts.ModuleResolutionKind.Bundler },
].flatMap((resolution) => [true, false].map((skipLibCheck) => ({
  name: `moduleResolution ${ts.ModuleResolutionKind[resolution.moduleResolution]}, skipLibCheck ${skipLibCheck}`,
  skipLibCheck,
  options: { ...resolution, skipLibCheck, strict: true, noEmit: true, target: ts.ScriptTarget.ES2022 },
})));

// typesPackage reads this package as npm packs it: package.json and the files it lists, as "<name>/<path>" -> text.
export function typesPackage() {
  const manifest = JSON.parse(readFileSync(join(sdk, "package.json"), "utf8"));
  const files = new Map([[`${TYPES}/package.json`, JSON.stringify(manifest)]]);
  for (const file of manifest.files) files.set(`${TYPES}/${file}`, readFileSync(join(sdk, file), "utf8"));
  return files;
}

// project writes a project directory and returns it: `files` (path -> text) and `links` (path -> the directory it links to, absolute or
// relative to the project), paths relative to the project; a path that begins with `../` is beside the project, under no ancestor of
// it but the temporary directory both are in. `discard` removes it all.
export function project({ files, links = new Map() }) {
  const directory = join(realpathSync(mkdtempSync(join(tmpdir(), "pig-author-view-"))), "project");
  mkdirSync(directory);
  writeFileSync(join(directory, "package.json"), '{"type":"module"}');
  for (const [path, text] of files) {
    mkdirSync(dirname(join(directory, path)), { recursive: true });
    writeFileSync(join(directory, path), text);
  }
  for (const [path, target] of links) {
    mkdirSync(dirname(join(directory, path)), { recursive: true });
    symlinkSync(resolve(directory, target), join(directory, path), "junction");
  }
  return directory;
}

export function discard(directory) {
  rmSync(dirname(directory), { recursive: true, force: true });
}

const under = (prefix, entries) => [...entries].map(([path, value]) => [`${prefix}/${path}`, value]);

// The ways the types package reaches an author's node_modules. Each takes `release` (the published Pi packages, "<package>/<path>" ->
// text), `types` (this package, the same shape), `others` (every other installed package -> its directory) and `sources`, and returns
// the { files, links } of a project. The trees are built by hand in the shape the installer gives them.
export const LAYOUTS = {
  // npm's default: every package of the closure is hoisted into the project's node_modules, the types package a copy among them.
  "hoisted (npm)": ({ release, types, others, sources }) => ({
    files: new Map([...under("node_modules", release), ...under("node_modules", types), ...sources]),
    links: new Map(under("node_modules", others)),
  }),
  // pnpm's shape: a package sees its own declared dependencies and nothing else. The types package sits in a store directory beside
  // links to its peer dependencies only; the project's node_modules links to it and to Pi.
  "isolated (pnpm)": ({ release, types, others, sources }) => {
    const pi = ".store/pi/node_modules";
    const mine = ".store/types/node_modules";
    const links = new Map(under(pi, others));
    for (const peer of Object.keys(JSON.parse(types.get(`${TYPES}/package.json`)).peerDependencies)) {
      if (release.has(`${peer}/package.json`)) links.set(`${mine}/${peer}`, `${pi}/${peer}`);
      else if (others.has(peer)) links.set(`${mine}/${peer}`, others.get(peer));
      else throw new Error(`the peer dependency ${peer} is not installed`);
    }
    links.set(`node_modules/${TYPES}`, `${mine}/${TYPES}`);
    links.set("node_modules/@earendil-works/pi-coding-agent", `${pi}/@earendil-works/pi-coding-agent`);
    links.set("node_modules/@types/node", others.get("@types/node"));
    return { files: new Map([...under(pi, release), ...under(mine, types), ...sources]), links };
  },
  // A link to the package's source folder, which `npm install <folder>` makes, in a project whose tsconfig sets preserveSymlinks: the
  // compiler then resolves the bundle's imports from node_modules and not from the folder, which is outside the project.
  "linked source folder (npm install <folder>, preserveSymlinks)": ({ release, types, others, sources }) => ({
    files: new Map([...under("node_modules", release), ...[...types].map(([path, text]) => [`../source/${path.slice(TYPES.length + 1)}`, text]), ...sources]),
    links: new Map([...under("node_modules", others), [`node_modules/${TYPES}`, "../source"]]),
    options: { preserveSymlinks: true },
  }),
};

// hoisted lists what npm installs beside a project's Pi: package -> directory for every package under the given node_modules
// directories (the first that has it), without the packages `except` names.
export function hoisted(directories, except) {
  const links = new Map();
  const add = (name, directory) => {
    if (!links.has(name) && !except(name)) links.set(name, directory);
  };
  for (const directory of directories) {
    for (const name of existsSync(directory) ? readdirSync(directory) : []) {
      if (name.startsWith(".")) continue;
      if (!name.startsWith("@")) add(name, join(directory, name));
      else for (const scoped of readdirSync(join(directory, name))) add(`${name}/${scoped}`, join(directory, name, scoped));
    }
  }
  return links;
}

// A path under a node_modules that is neither this package nor one of Pi's four.
const OTHERS = new RegExp(`node_modules/(?!${TYPES}/|@earendil-works/(?:${PACKAGES.join("|")})/)`);

// compile type-checks source files of a project: { program, diagnostics }. The diagnostics are those of the project's sources, of this
// package and of Pi's four packages, each { file (relative to the project), code, text }.
export function compile(directory, files, options) {
  const program = ts.createProgram(files.map((file) => join(directory, file)), { ...options, typeRoots: [join(directory, "node_modules", "@types")] });
  const found = [...program.getOptionsDiagnostics(), ...program.getGlobalDiagnostics()];
  const root = `${directory.split("\\").join("/")}/`;
  for (const source of program.getSourceFiles()) {
    if (source.fileName.startsWith(root) && !OTHERS.test(source.fileName.slice(root.length))) found.push(...program.getSyntacticDiagnostics(source), ...program.getSemanticDiagnostics(source));
  }
  const diagnostics = found.map((diagnostic) => ({
    file: diagnostic.file ? relative(directory, diagnostic.file.fileName).split("\\").join("/") : "",
    code: diagnostic.code,
    text: ts.flattenDiagnosticMessageText(diagnostic.messageText, " "),
  }));
  return { program, diagnostics };
}

// codes lists the diagnostic codes reported for one source file.
export function codes(diagnostics, file) {
  return diagnostics.filter((diagnostic) => diagnostic.file === file).map((diagnostic) => diagnostic.code);
}

// declared returns the type an ambient module of a program exports under a name, or undefined.
export function declared(program, specifier, name) {
  const checker = program.getTypeChecker();
  const module = checker.getAmbientModules().find((symbol) => symbol.name === `"${specifier}"`);
  const exported = module && checker.getExportsOfModule(module).find((symbol) => symbol.name === name);
  if (!exported) return undefined;
  const target = exported.flags & ts.SymbolFlags.Alias ? checker.getAliasedSymbol(exported) : exported;
  return target.flags & ts.SymbolFlags.Value && !(target.flags & ts.SymbolFlags.Class) ? checker.getTypeOfSymbol(target) : checker.getDeclaredTypeOfSymbol(target);
}

// ambient lists the modules a program declares ambiently: name -> the files (relative to the project) that declare it.
export function ambient(program, directory) {
  const checker = program.getTypeChecker();
  const files = (symbol) => symbol.declarations.map((declaration) => relative(directory, declaration.getSourceFile().fileName).split("\\").join("/"));
  return new Map(checker.getAmbientModules().map((symbol) => [symbol.name.slice(1, -1), files(symbol)]));
}

// defaults type-checks one module that imports each of the given files (absolute paths) as a default import, and returns the type of
// each import in full.
export function defaults(directory, paths, options) {
  const file = "defaults.ts";
  writeFileSync(join(directory, file), paths.map((path, at) => `import value${at} from ${JSON.stringify(path)};\nexport const used${at} = value${at};\n`).join(""));
  const { program } = compile(directory, [file], options);
  const declarations = program.getSourceFile(join(directory, file)).statements.filter(ts.isVariableStatement).map((statement) => statement.declarationList.declarations[0].name);
  return declarations.map((name) => text(program, program.getTypeChecker().getTypeAtLocation(name)));
}

// text prints a type in full.
export function text(program, type) {
  return program.getTypeChecker().typeToString(type, undefined, ts.TypeFormatFlags.NoTruncation);
}
