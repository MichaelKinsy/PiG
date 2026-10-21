#!/usr/bin/env node
// Generate the Pi vectors the SDK helper ports are tested against.
//
//   node automation/gen/generate-sdk-vectors.mjs            # write every vector file
//   node automation/gen/generate-sdk-vectors.mjs --check    # fail if a checked-in file differs
//   node automation/gen/generate-sdk-vectors.mjs pi-tui/truncateToWidth ...   # only these
//
// A case source test/parity/sdk-vectors/cases/<module-dir>/<export>.mjs exports
//   cases: [{ name, args: [...] }]            JSON-encodable inputs (see encode below)
//   call?: (fn, ...args) => result            default: fn(...args); use it to reach a class member
//                                              or to build an input an argument list cannot hold
// and this script runs Pi's own function, from the oracle the repo pins
// (python3 automation/pin/pin.py install-oracle; extensions/sdk-ts/node_modules), writing
// test/parity/sdk-vectors/<module-dir>/<export>.json. The result of Pi's code is the expected value:
// nothing here is hand-written. <module-dir> is the module with "/" as "-": pi-coding-agent, pi-tui,
// pi-ai, pi-ai-compat, pi-agent-core. See test/parity/sdk-vectors/README.md.
//
// A `call` that is a `function` (not an arrow) runs with `this` bound to { load(moduleDir) }, the namespace of any served module, for the
// state a global helper reads (pi-tui's keybindings, the coding-agent theme): `const tui = await this.load("pi-tui")`.
// chalk, which draws the theme's bold and italic, decides its level when it is first imported, so the level is fixed here, not by the terminal
// the generator runs in.
import { readFileSync, readdirSync, writeFileSync, mkdirSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

process.env.FORCE_COLOR = "3";
const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const vectors = join(root, "test/parity/sdk-vectors");
const oracle = join(root, "extensions/sdk-ts/node_modules");
const pin = /^version=(.+)$/m.exec(readFileSync(join(root, ".upstream/pin"), "utf8"))[1].trim();

// module-dir -> [npm package, entrypoint]
const modules = {
  "pi-coding-agent": ["pi-coding-agent", "."],
  "pi-tui": ["pi-tui", "."],
  "pi-ai": ["pi-ai", "."],
  "pi-ai-compat": ["pi-ai", "./compat"],
  "pi-agent-core": ["pi-agent-core", "."],
};

function packageDir(name) {
  const base = join(oracle, "@earendil-works");
  for (const dir of [join(base, name), join(base, "pi-coding-agent/node_modules/@earendil-works", name)]) {
    if (existsSync(join(dir, "package.json"))) return dir;
  }
  throw new Error(`oracle package @earendil-works/${name} not found under ${oracle}: run python3 automation/pin/pin.py install-oracle`);
}

async function load(moduleDir) {
  const [name, entry] = modules[moduleDir];
  const dir = packageDir(name);
  const pkg = JSON.parse(readFileSync(join(dir, "package.json"), "utf8"));
  let target = pkg.exports?.[entry];
  if (target && typeof target === "object") target = target.import?.default ?? target.import ?? target.default;
  if (typeof target === "object") target = target?.default;
  if (target === undefined && entry === ".") target = pkg.module ?? pkg.main;
  if (typeof target !== "string") throw new Error(`${name} exports no ${entry}`);
  return { ns: await import(pathToFileURL(join(dir, target)).href), version: pkg.version };
}

// encode makes a value JSON: undefined, NaN, the infinities, bigints and symbols become {"$": "<kind>"} (with "value"
// for the last two); a Map or Set becomes {"$": "map"|"set", "value": [...]}; everything else is JSON as is. A
// consumer decodes the same tags.
function encode(v) {
  if (v === undefined) return { $: "undefined" };
  if (typeof v === "number" && !Number.isFinite(v)) return { $: Number.isNaN(v) ? "NaN" : v > 0 ? "Infinity" : "-Infinity" };
  if (typeof v === "bigint") return { $: "bigint", value: v.toString() };
  if (typeof v === "symbol") return { $: "symbol", value: v.description ?? "" };
  if (typeof v === "function") throw new Error("a vector cannot hold a function");
  if (v instanceof Map) return { $: "map", value: [...v].map(([k, x]) => [encode(k), encode(x)]) };
  if (v instanceof Set) return { $: "set", value: [...v].map(encode) };
  if (Array.isArray(v)) return v.map(encode);
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, encode(x)]));
  return v;
}

function* caseSources() {
  for (const moduleDir of readdirSync(join(vectors, "cases"), { withFileTypes: true })) {
    if (!moduleDir.isDirectory()) continue;
    for (const f of readdirSync(join(vectors, "cases", moduleDir.name)).sort()) {
      if (f.endsWith(".mjs")) yield [moduleDir.name, f.slice(0, -4)];
    }
  }
}

const args = process.argv.slice(2);
const check = args.includes("--check");
const only = args.filter((a) => !a.startsWith("--"));
const cache = new Map();
let stale = 0;
const env = { load: async (moduleDir) => {
  if (!cache.has(moduleDir)) cache.set(moduleDir, await load(moduleDir));
  return cache.get(moduleDir).ns;
} };
if (!existsSync(join(vectors, "cases"))) process.exit(0);
for (const [moduleDir, name] of caseSources()) {
  if (only.length && !only.includes(`${moduleDir}/${name}`)) continue;
  if (!modules[moduleDir]) throw new Error(`cases/${moduleDir}: not a served module (${Object.keys(modules).join(", ")})`);
  if (!cache.has(moduleDir)) cache.set(moduleDir, await load(moduleDir));
  const { ns, version } = cache.get(moduleDir);
  const source = await import(pathToFileURL(join(vectors, "cases", moduleDir, `${name}.mjs`)).href);
  const fn = ns[name.split(".")[0]];
  if (fn === undefined) throw new Error(`${moduleDir} serves no ${name}`);
  const call = source.call ?? ((f, ...a) => f(...a));
  const out = [];
  for (const c of source.cases) {
    const entry = { name: c.name, args: encode(c.args) };
    try {
      entry.result = encode(await call.call(env, fn, ...c.args));
    } catch (err) {
      entry.throws = String(err?.message ?? err);
    }
    out.push(entry);
  }
  const text = JSON.stringify({ pin, package: `${modules[moduleDir][0]}@${version}`, module: moduleDir, export: name, cases: out }, null, 1) + "\n";
  const path = join(vectors, moduleDir, `${name}.json`);
  if (check) {
    if (!existsSync(path) || readFileSync(path, "utf8") !== text) {
      console.error(`stale: ${path}`);
      stale++;
    }
  } else {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, text);
  }
}
process.exit(stale ? 1 : 0);
