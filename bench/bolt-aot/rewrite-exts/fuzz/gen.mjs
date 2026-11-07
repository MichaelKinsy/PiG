// SPDX-License-Identifier: MIT
// Generator for the differential fuzz of the Piglet's rewrite (rewrite.go) against Pi-Bolt's rewriteCopy (see run.sh and compare.mjs).
// Each module is a sequence of independent fragments. A fragment is written with its expected rewrite beside it, by construction and not by running either
// implementation: the target expressions (import.meta.url, __dirname, ...) become literals for the module's path, and everything that only looks like one (the same text in
// a string, a comment, a template, a regular expression or JSX text, a member's name, a TypeScript-only position) stays. What a fragment needs from the lexer is the point:
// each one puts a `/` after a token that makes it divide or begin a regular expression, so that a wrong guess hides a target in a regexp or exposes a trap as code.
// usage: bun gen.mjs OUT.json [modules=4050] [seed=1]
import { pathToFileURL } from "node:url";
import { writeFileSync } from "node:fs";

const [outPath, modulesArg, seedArg] = process.argv.slice(2);
const MODULES = Number(modulesArg ?? 4050);
let seed = Number(seedArg ?? 1) >>> 0;
const rnd = () => ((seed = (seed + 0x6d2b79f5) >>> 0), ((t) => ((t = Math.imul(t ^ (t >>> 15), t | 1)), (t ^= t + Math.imul(t ^ (t >>> 7), t | 61)), ((t ^ (t >>> 14)) >>> 0) / 4294967296))(seed));
const pick = (list) => list[Math.floor(rnd() * list.length)];
const chance = (p) => rnd() < p;

const DIR = "/ext/p/dir";
const EXTS = [["ts", 40], ["mjs", 15], ["js", 15], ["cjs", 10], ["tsx", 10], ["jsx", 10]];
const isTS = (e) => e === "ts" || e === "tsx";
const hasJSX = (e) => e === "tsx" || e === "jsx" || e === "js" || e === "mjs" || e === "cjs";

// Target kinds and what each is for the module at `file`.
const META = { url: "url", dirname: "dirname", filename: "filename", dir: "dir", path: "path", file: "file" };
function value(kind, file) {
  switch (kind) {
    case "url": return JSON.stringify(pathToFileURL(file).href);
    case "dirname": case "dir": case "__dirname": return JSON.stringify(DIR);
    case "filename": case "path": case "__filename": return JSON.stringify(file);
    case "file": return JSON.stringify(file.slice(file.lastIndexOf("/") + 1));
  }
  throw new Error(kind);
}
const T = (kind) => ({ k: kind });
const TEXT = { url: "import.meta.url", dirname: "import.meta.dirname", filename: "import.meta.filename", dir: "import.meta.dir", path: "import.meta.path", file: "import.meta.file", __dirname: "__dirname", __filename: "__filename" };

// Render parts for a module at `file` whose declared names are `declared`: a target that is a free name stays when the module declares it (name-wide, as both implementations decide).
function render(parts, file, declared) {
  let src = "", exp = "";
  for (const p of parts) {
    if (typeof p === "string") { src += p; exp += p; continue; }
    if (p.k.startsWith("sh:")) { // an object literal's shorthand: { __dirname }
      const name = p.k.slice(3);
      src += name; exp += declared.has(name) ? name : `${name}: ${value(name, file)}`;
      continue;
    }
    src += TEXT[p.k];
    exp += (p.k.startsWith("__") && declared.has(p.k)) ? TEXT[p.k] : value(p.k, file);
  }
  return { src, exp };
}

const TRAPS = ["import.meta.url", "__dirname", "__filename", "import.meta.dirname", "require(x)", "require.resolve(y)", "import(x)", "import.meta.resolve(z)"];
const trap = () => pick(TRAPS);
const anyTarget = (ext, declaredNames) => {
  const kinds = ["__dirname", "__filename"];
  if (ext !== "cjs") kinds.push(...Object.keys(META));
  return T(pick(kinds));
};

// --- families ---

const VALUE_ENDERS = ["i++", "i--", "a", "a.b", "a[0]++", "o.p--", "f()", "f(x).y", "f(1)(2)", "a[0]", "(a)", "(a, b)", "({a:1})", "1", "0x1f", "1.5e3", "'s'", '"s"', "`t`", "`t${a}`", "/re/g", "this", "null", "true", "new X()", "a?.[0]", "a?.b"];
const KEYWORD_PROPS = ["return", "typeof", "instanceof", "in", "of", "new", "delete", "void", "throw", "case", "do", "else", "yield", "await", "extends", "if", "while", "for", "with", "class", "function"];
function valueEnder(ext) {
  const r = rnd();
  if (r < 0.35) return pick(VALUE_ENDERS);
  if (r < 0.75) return `${pick(["o", "x.y", "a"])}${chance(0.5) ? "." : "?."}${pick(KEYWORD_PROPS)}`;
  if (isTS(ext) && r < 0.85) return pick(["x!", "a.b!", "f()!"]);
  return pick(VALUE_ENDERS);
}
// Division: the `/` after a value divides, so a target later on the line is code.
function division(ext, n) {
  const v = valueEnder(ext);
  const form = Math.floor(rnd() * 3);
  const tg = anyTarget(ext);
  if (form === 0) return [`${v} / 2, u${n} = `, tg, ` / 3;\n`];
  if (form === 1) return [`const d${n} = ${v} / 2 + (`, tg, `) / 4;\n`];
  return [`d${n} = ${v}\n  / 2, e${n} = `, tg, ` / 5;\n`];
}

const REGEX_BODIES = [(t) => t, (t) => `[/]${t}`, (t) => `a\\/b ${t}`, (t) => `${t}|x`, (t) => `[^/]*${t}`];
const regex = () => `/${pick(REGEX_BODIES)(trap())}/${pick(["", "g", "i", "gi"])}`;
// Regex: the `/` after an operator or a statement keyword begins a regular expression, so the trap inside it is no code and the target after it is.
const REGEX_FORMS = [
  (r, t, n) => [`x${n} = ${r}.test(y), u${n} = `, t, `;\n`],
  (r, t, n) => [`f(${r}.test(y), `, t, `);\n`],
  (r, t, n) => [`[a, ${r}.test(y), `, t, `];\n`],
  (r, t, n) => [`function g${n}() { return ${r}.test(y) && `, t, `; }\n`],
  (r, t, n) => [`a && ${r}.test(y), `, t, `;\n`],
  (r, t, n) => [`a || ${r}.test(y), `, t, `;\n`],
  (r, t, n) => [`!${r}.test(y), `, t, `;\n`],
  (r, t, n) => [`c ? ${r}.test(y) : `, t, `;\n`],
  (r, t, n) => [`void ${r}, `, t, `;\n`],
  (r, t, n) => [`typeof ${r}, `, t, `;\n`],
  (r, t, n) => [`a++ + ${r}.test(y), `, t, `;\n`],
  (r, t, n) => [`a-- - ${r}.test(y), `, t, `;\n`],
  (r, t, n) => [`a + +b + ${r}.test(y), `, t, `;\n`],
  (r, t, n) => [`++${r}.lastIndex, `, t, `;\n`],
  (r, t, n) => [`if (c) ${r}.test(y);\n`, t, `;\n`],
  (r, t, n) => [`while (c) ${r}.test(y);\n`, t, `;\n`],
  (r, t, n) => [`for (;;) ${r}.test(y);\n`, t, `;\n`],
  (r, t, n) => [`{}\n${r}.test(y);\n`, t, `;\n`],
  (r, t, n) => [`function h${n}() {}\n${r}.test(y);\n`, t, `;\n`],
  (r, t, n) => [`const k${n} = () => ${r}.test(y), u${n} = `, t, `;\n`],
  (r, t, n) => [`x = { a: ${r}.test(y), b: `, t, ` };\n`],
  (r, t, n) => [`switch (c) { case ${r}.source: `, t, `; }\n`],
  (r, t, n) => [`"a" in ${r}, `, t, `;\n`],
  (r, t, n) => [`for (const q of ${r}.source) `, t, `;\n`],
  (r, t, n) => [`async function w${n}() { await ${r}.exec(y); `, t, `; }\n`],
  (r, t, n) => [`if (c) ; else ${r}.test(y);\n`, t, `;\n`],
  (r, t, n) => [`do ${r}.test(y); while (c);\n`, t, `;\n`],
  (r, t, n) => [`x = \`\${${r}.source}\`, `, t, `;\n`],
  (r, t, n) => [`function t${n}() { throw ${r}.x; }\n`, t, `;\n`],
];
function regexFragment(ext, n) {
  const form = pick(REGEX_FORMS);
  return form(regex(), anyTarget(ext), n);
}

const TEXT_FORMS = [
  (x, t, n) => [`const s${n} = "${x}"; use(`, t, `);\n`],
  (x, t, n) => [`const s${n} = '${x}'; use(`, t, `);\n`],
  (x, t, n) => [`const s${n} = "a\\" ${x}"; use(`, t, `);\n`],
  (x, t, n) => [`const s${n} = "a\\\r\n${x}"; use(`, t, `);\n`],
  (x, t, n) => [`const s${n} = "a\u2028${x}"; use(`, t, `);\n`],
  (x, t, n) => ["const s" + n + " = `" + x + "`; use(", t, ");\n"],
  (x, t, n) => ["const s" + n + " = `" + x + " ${", t, "} " + x + "`;\n"],
  (x, t, n) => ["const s" + n + " = `a${`b${", t, "} " + x + "`} " + x + "`;\n"],
  (x, t, n) => ["const s" + n + " = `${ {a: 1}.a } " + x + " ${", t, "}`;\n"],
  (x, t, n) => [`// ${x}\nuse(`, t, `);\n`],
  (x, t, n) => [`/* ${x}\n${x} */ use(`, t, `);\n`],
  (x, t, n) => [`// ${x}\r`, "use(", t, ");\n"],
  (x, t, n) => [`// ${x}\u2028`, "use(", t, ");\n"],
  (x, t, n) => [`// ${x}\u2029`, "use(", t, ");\n"],
  (x, t, n) => [`/* ${x} */ /* ${x} */ use(`, t, `); // ${x}\n`],
  (x, t, n) => [`use(/*${x}*/`, t, `/*${x}*/);\n`],
  (x, t, n) => [`const r${n} = ${pick(["/", "/"])}${x.replace(/\//g, "\\/")}/g; use(`, t, `);\n`],
];
function textFragment(ext, n) { return pick(TEXT_FORMS)(trap(), anyTarget(ext), n); }

function jsxFragment(ext, n) {
  const x = trap(), t = anyTarget(ext);
  return pick([
    [`const j${n} = <div a="${x}">${x} {`, t, `}</div>;\n`],
    [`const j${n} = <><b>${x}</b>{`, t, `}</>;\n`],
    [`const j${n} = <A b={`, t, `} c='${x}' />;\n`],
    [`function J${n}() { return <ul>${x}{[1].map((i) => <li key={i}>{`, t, `}</li>)}</ul>; }\n`],
  ]);
}

// Names the module declares: both implementations treat a declaration as covering the whole module.
function declarationFragment(name, ext, n) {
  const forms = [
    [`const ${name} = "x";\n`],
    [`let ${name};\n`],
    [`var ${name} = f(`, T("url"), `);\n`],
    [`function ${name}() {}\n`],
    [`class ${name} {}\n`],
    [`function p${n}(a, ${name}) { return ${name}; }\n`],
    [`const q${n} = (${name}, b) => ${name};\n`],
    [`const r${n} = async (${name} = 1) => ${name};\n`],
    [`const { ${name} } = ctx${n};\n`],
    [`const { a${n}, ${name} } = ctx;\n`],
    [`const { z${n}: { ${name} } } = ctx;\n`],
    [`const [${name}] = list${n};\n`],
    [`function s${n}({ ${name}, b }) { return ${name}; }\n`],
    [`try {} catch (${name}) { use(${name}); }\n`],
    [`${name} = "/x";\n`],
  ];
  if (ext !== "cjs") forms.push([`import { ${name} } from "./m${n}.js";\n`], [`import * as ${name} from "./n${n}.js";\n`], [`import d${n}, { ${name} } from "./o${n}.js";\n`], [`import { a${n} as ${name} } from "./p${n}.js";\n`]);
  if (isTS(ext)) forms.push([`declare const ${name}: string;\n`], [`function t${n}(a: string, ${name}: string): string { return ${name}; }\n`], [`class C${n} { constructor(private ${name}: string) {} }\n`], [`class D${n} { constructor(public readonly ${name}: string, protected b: number) {} }\n`]);
  return pick(forms);
}

// A free use of a name that only looks like a declaration or a binding: it is rewritten (unless the module declares the name elsewhere).
function lookalikeFragment(ext, n) {
  const name = pick(["__dirname", "__filename"]);
  const k = T(name);
  return pick([
    [`use(a, `, k, `, b);\n`],
    [`use(`, k, `);\n`],
    [`if (`, k, `) { go(); }\n`],
    [`while (`, k, `) {}\n`],
    [`for (const q${n} of [`, k, `]) {}\n`],
    [`const o${n} = { a, `, { k: "sh:" + name }, ` };\n`],
    [`run({ `, { k: "sh:" + name }, ` });\n`],
    [`const arr${n} = [x, `, k, `, y];\n`],
    [`const e${n} = `, k, ` == other, f${n} = () => `, k, `;\n`],
    [`join(root, `, k, `, "x");\n`],
    [`const t${n} = c ? join(`, k, `, "a") : { b: 1 };\n`],
    [`switch (c) { case f(`, k, `): { go(); } }\n`],
    [`x.${name}; x?.${name}; o.${name}(1);\n`],
    [`const m${n} = { ${name}: 1, ${name === "__dirname" ? "__filename" : "__dirname"}() {} };\n`],
    [`class K${n} { async import(u) {} require(id) { return 1 } static require(id) { return 2 } get ${name}() { return 4 } }\n`],
  ]);
}

// TypeScript-only and member positions: the name is not a use of the free variable.
function positionFragment(ext, n) {
  const name = pick(["__dirname", "__filename"]);
  const other = name === "__dirname" ? "__filename" : "__dirname";
  const ts = [
    [`const v${n} = x satisfies typeof ${name};\n`],
    [`function f${n}(): typeof ${name} { return ""; }\n`],
    [`type R${n} = Record<string, typeof ${name}>;\n`],
    [`type K${n} = keyof typeof ${name};\n`],
    [`let rr${n}: typeof require.resolve;\n`],
    [`enum E${n} { ${name}, ${other} = 1 }\n`],
    [`const enum F${n} { A, ${name} }\n`],
    [`interface I${n} { ${name}: string; readonly ${other}?: string; require(id: string): void }\n`],
    [`type L${n} = { ${name}?: string; ${other}: string };\n`],
    [`export { x${n} as ${name} };\n`],
    [`export { ${name} as d${n} } from "./m${n}.js";\n`],
    [`export { a${n}, ${other}, b${n} as ${name} } from "./m${n}.js";\n`],
    [`export * as ${name} from "./m${n}.js";\n`],
    [`export type { ${name} as t${n} } from "./m${n}.js";\n`],
    // A type annotation with typeof is a type position, not an assignment to or a declaration of the name: the use after it is still a free use.
    [`const x${n}: typeof ${name} = "a";\nuse(`, T(name), `);\n`],
    [`let x${n}: typeof ${name} = "a", y${n}: typeof ${other} = "b";\nuse(`, T(name), `, `, T(other), `);\n`],
    [`var x${n}: typeof ${name} = "a";\nuse(`, T(name), `);\n`],
    [`export const x${n}: typeof ${name} = "a";\nexport const y${n} = `, T(name), `;\n`],
    [`function f${n}(a: typeof ${name} = "x") { return a; }\nuse(`, T(name), `);\n`],
    [`const g${n} = (a: typeof ${name} = "x") => a;\nuse(`, T(name), `);\n`],
    [`class C${n} { x: typeof ${name} = "a"; static y: typeof ${other} = "b" }\nuse(`, T(name), `, `, T(other), `);\n`],
    [`const o${n}: Record<string, typeof ${name}> = {};\nuse(`, T(name), `);\n`],
    [`let r${n}: (typeof ${name})[] = [];\nuse(`, T(name), `);\n`],
  ];
  const any = [
    [`class C${n} { ${name}; }\n`],
    [`class C${n} { ${name} = 1 }\n`],
    [`class C${n} { static ${other} = 2; ${name}\n  ${other}\n}\n`],
    [`class C${n} extends B${n} { ${name} = 1 }\n`],
    [`const C${n} = class { ${name} = 1 };\n`],
    [`class C${n} { m() {}\n  ${name}\n  ${other}\n}\n`],
    [`const o${n} = { ${name}: 1, ${other}() {} };\n`],
  ];
  const f = isTS(ext) ? pick([...ts, ...any]) : pick(any);
  return f;
}

// Modules and fragments: a fragment that stands alone is a test of its own (the minimal repro).
function makeFragment(ext, n, declared) {
  const r = rnd();
  if (r < 0.26) return ["division", division(ext, n)];
  if (r < 0.52) return ["regex", regexFragment(ext, n)];
  if (r < 0.68) return ["text", textFragment(ext, n)];
  if (r < 0.74 && hasJSX(ext)) return ["jsx", jsxFragment(ext, n)];
  if (r < 0.90) return ["lookalike", lookalikeFragment(ext, n)];
  return ["position", positionFragment(ext, n)];
}

const items = [];
const singles = new Map();
let id = 0;
for (let m = 0; m < MODULES; m++) {
  const ext = (() => { let r = rnd() * 100; for (const [e, w] of EXTS) { if ((r -= w) < 0) return e; } return "ts"; })();
  const file = `${DIR}/mod.${ext}`;
  const declared = new Set();
  for (const name of ["__dirname", "__filename"]) if (chance(0.15)) declared.add(name);
  const count = 3 + Math.floor(rnd() * 6);
  const parts = [];
  const tags = [];
  for (let i = 0; i < count; i++) {
    const [tag, frag] = makeFragment(ext, m * 10 + i, declared);
    tags.push(tag);
    parts.push(...frag);
    // The same fragment alone, for the minimal repro: its declared set is empty unless it is a declaration of the names it mentions.
    const alone = render(frag, file, new Set());
    const key = alone.src.replace(/\d+/g, "N") + "\0" + ext;
    if (!singles.has(key)) singles.set(key, { file, tag, ...alone });
  }
  // A declaring fragment covers the module: put one in whenever the module declares a name.
  for (const name of declared) { const frag = declarationFragment(name, ext, m * 10 + 9); parts.unshift(...frag); tags.unshift("declaration"); }
  const { src, exp } = render(parts, file, declared);
  items.push({ id: id++, kind: "module", file, src, exp, tags });
}
// Thirteen hand cases: whole modules of the shapes the two real packages and common CommonJS have.
const add = (name, file, src, exp) => items.push({ id: id++, kind: "hand", file, src, exp, tags: [name] });
const f1 = `${DIR}/mod.mjs`, f2 = `${DIR}/mod.cjs`, f3 = `${DIR}/mod.ts`;
add("esm idiom", f1, `import { dirname, join } from "node:path";\nimport { fileURLToPath } from "node:url";\nconst __dirname = dirname(fileURLToPath(import.meta.url));\nexport const p = join(__dirname, "data");\n`,
  `import { dirname, join } from "node:path";\nimport { fileURLToPath } from "node:url";\nconst __dirname = dirname(fileURLToPath(${value("url", f1)}));\nexport const p = join(__dirname, "data");\n`);
add("new URL beside", f3, `export const u = new URL("./locales/de.json", import.meta.url);\n`, `export const u = new URL("./locales/de.json", ${value("url", f3)});\n`);
add("cjs dirname", f2, `const fs = require("fs");\nmodule.exports = fs.readFileSync(__dirname + "/x");\n`, `const fs = require("fs");\nmodule.exports = fs.readFileSync(${JSON.stringify(DIR)} + "/x");\n`);
add("cjs both", f2, `module.exports = [__dirname, __filename];\n`, `module.exports = [${JSON.stringify(DIR)}, ${JSON.stringify(f2)}];\n`);
add("meta members", f1, `export default [import.meta.dirname, import.meta.filename, import.meta.dir, import.meta.path, import.meta.file];\n`, `export default [${JSON.stringify(DIR)}, ${JSON.stringify(f1)}, ${JSON.stringify(DIR)}, ${JSON.stringify(f1)}, ${JSON.stringify("mod.mjs")}];\n`);
add("template substitution", f3, "export const s = `${import.meta.dirname}/x ${__filename}`;\n", "export const s = `${" + JSON.stringify(DIR) + "}/x ${" + JSON.stringify(f3) + "}`;\n");
add("comment and string", f3, `// import.meta.url\nexport const s = "__dirname"; /* __filename */\n`, `// import.meta.url\nexport const s = "__dirname"; /* __filename */\n`);
add("regex then use", f3, `const re = /import.meta.url/g;\nexport const d = __dirname;\n`, `const re = /import.meta.url/g;\nexport const d = ${JSON.stringify(DIR)};\n`);
add("division then use", f3, `const h = total / 2 / count;\nexport const d = __dirname;\n`, `const h = total / 2 / count;\nexport const d = ${JSON.stringify(DIR)};\n`);
add("class with methods", f3, `export class A { async import(u: string) { return u } require(id: string) { return id } }\n`, `export class A { async import(u: string) { return u } require(id: string) { return id } }\n`);
add("static specifiers", f1, `const fs = require("fs"); const m = await import("./a.js");\n`, `const fs = require("fs"); const m = await import("./a.js");\n`);
add("object key", f3, `export const o = { __dirname: 1, __filename() {}, a: import.meta.url };\n`, `export const o = { __dirname: 1, __filename() {}, a: ${value("url", f3)} };\n`);
add("shorthand", f3, `export const o = { a, __dirname };\n`, `export const o = { a, __dirname: ${JSON.stringify(DIR)} };\n`);


for (const s of singles.values()) items.push({ id: id++, kind: "fragment", file: s.file, src: s.src, exp: s.exp, tags: [s.tag] });
writeFileSync(outPath, JSON.stringify(items));
console.log(`${items.filter((i) => i.kind === "module").length} modules, ${singles.size} fragments`);
