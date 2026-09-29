// Runtime probe for the Node extension runtime (go run ./test/parity/cmd/sdksurface).
//
// It loads the runtime the way an extension sees it and reports what exists:
//
//   - the members of the objects an extension reaches (pi, ctx, ctx.ui,
//     ctx.ui.theme, ctx.sessionManager, ctx.modelRegistry), by instantiating
//     the runtime and walking each object's own and inherited properties;
//   - for every runtime export Pi's packages declare, and every member of each
//     exported class, whether the runtime module serves it, and where the
//     value's code comes from: Pi's own code copied into shims/pi-dist (or the
//     third-party packages Pi depends on), PiG's own implementation in the
//     runtime modules, or a stand-in that throws when called or constructed.
//
// Function origins come from the V8 inspector ([[FunctionLocation]] mapped to
// the parsed script's URL), not from reading the source files.
//
// usage: node probe.mjs <runtime-node dir> <request.json> <output.json>
// (modules it imports may write to stdout, so the report goes to a file)
import { readFile, writeFile } from "node:fs/promises";
import { readdirSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { Session } from "node:inspector/promises";

const [runtimeDir, requestPath, outputPath] = process.argv.slice(2);
// Pi's vendored CLI modules read process.argv on import.
process.argv = process.argv.slice(0, 2);
const request = JSON.parse(await readFile(requestPath, "utf8"));
const out = { symbols: [], packages: {} };

const session = new Session();
session.connect();
const scripts = new Map();
session.on("Debugger.scriptParsed", ({ params }) => scripts.set(params.scriptId, params.url));
await session.post("Debugger.enable");

async function functionURL(fn) {
  globalThis.__sdkSurfaceProbe = fn;
  try {
    const { result } = await session.post("Runtime.evaluate", { expression: "globalThis.__sdkSurfaceProbe" });
    if (!result.objectId) return "";
    const { internalProperties } = await session.post("Runtime.getProperties", { objectId: result.objectId, ownProperties: true });
    const location = internalProperties?.find((p) => p.name === "[[FunctionLocation]]")?.value?.value;
    return location ? scripts.get(location.scriptId) ?? "" : "";
  } finally {
    delete globalThis.__sdkSurfaceProbe;
  }
}

const standInRE = /not available to extensions|throw hostOnly/;

// origin classifies a function by where its code lives.
async function origin(fn) {
  if (standInRE.test(Function.prototype.toString.call(fn))) return "stand-in";
  const url = await functionURL(fn);
  if (url.includes("/shims/pi-dist/") || /\/shims\/(highlight\.js|marked|yaml|get-east-asian-width|partial-json|typebox)/.test(url)) return "vendored";
  if (url.includes("/runtime-node/")) return "bridged";
  return url === "" ? "bridged" : "vendored";
}

function standInReason(value, isClass) {
  try {
    if (isClass) new value();
    else value();
  } catch (err) {
    return String(err?.message ?? err);
  }
  return "";
}

// ── Objects an extension reaches ────────────────────────────────────────────
const { Runtime } = await import(pathToFileURL(join(runtimeDir, "runtime.mjs")).href);
const runtime = new Runtime("/probe/extension.mjs");
runtime.ui.theme.setPalette({ name: "probe", sourcePath: "/probe/theme.json", foregrounds: {}, backgrounds: {} });

function memberNames(obj) {
  const names = new Set();
  for (let p = obj; p && p !== Object.prototype; p = Object.getPrototypeOf(p)) {
    for (const name of Object.getOwnPropertyNames(p)) if (name !== "constructor") names.add(name);
  }
  return names;
}
for (const name of Object.keys(runtime.api)) out.symbols.push(`api.${name}`);
for (const name of Object.keys(runtime.api.events ?? {})) out.symbols.push(`api.events.${name}`);
for (const obj of [runtime.ctx, runtime.ui, runtime.ui.theme, runtime.ctx.sessionManager, runtime.ctx.modelRegistry]) {
  const cls = obj.constructor.name;
  for (const name of memberNames(obj)) out.symbols.push(`${cls}.${name}`);
}

// ── Package exports ─────────────────────────────────────────────────────────
// Values Pi's own modules export, for non-function exports' origin.
const vendoredValues = new Set();
function jsFiles(dir) {
  const files = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) files.push(...jsFiles(path));
    else if (entry.name.endsWith(".js")) files.push(path);
  }
  return files;
}
for (const file of jsFiles(join(runtimeDir, "shims", "pi-dist")).sort()) {
  try {
    const ns = await import(pathToFileURL(file).href);
    for (const value of Object.values(ns)) if (value !== null && typeof value === "object") vendoredValues.add(value);
  } catch {
    // A module that cannot load on its own is reached through its importers.
  }
}

// memberKey resolves a declaration's computed member name, such as
// "[Symbol.iterator]" or a symbol the module exports ("[Symbol.LAYOUT_NODE]"
// is the declaration text for an exported LAYOUT_NODE symbol).
const { LAYOUT_NODE } = await import(pathToFileURL(join(runtimeDir, "shims/pi-dist/pi-tui/layout-node.js")).href);
const { VIEWPORT_TUI } = await import(pathToFileURL(join(runtimeDir, "shims/pi-dist/pi-tui/tui.js")).href);

function memberKey(name, ns) {
  const m = /^\[Symbol\.(\w+)\]$/.exec(name);
  if (!m) return name;
  if (m[1] === "LAYOUT_NODE") return LAYOUT_NODE;
  if (m[1] === "VIEWPORT_TUI") return VIEWPORT_TUI;
  if (typeof Symbol[m[1]] === "symbol") return Symbol[m[1]];
  if (typeof ns?.[m[1]] === "symbol") return ns[m[1]];
  for (const value of Object.values(ns ?? {})) if (typeof value === "symbol" && value.description === m[1]) return value;
  return name;
}

function findMember(cls, name) {
  for (let p = cls.prototype; p && p !== Object.prototype; p = Object.getPrototypeOf(p)) {
    const d = Object.getOwnPropertyDescriptor(p, name);
    if (d) return d.value ?? d.get ?? d.set ?? null;
  }
  return undefined;
}

function classSources(cls) {
  const sources = [];
  for (let c = cls; typeof c === "function" && c !== Function.prototype; c = Object.getPrototypeOf(c)) {
    sources.push(Function.prototype.toString.call(c));
  }
  return sources.join("\n");
}

const cause = new Error("surface probe cause");
// Valid non-default constructor inputs for the conditional Error.cause fields.
const errorProbeArgs = {
  ModelsError: ["auth", "probe", { cause }],
  CredentialSynchronizationError: ["provider", "login", undefined, { cause }],
  FileError: ["io", "probe", "/probe", cause],
  ExecutionError: ["exec", "probe", cause],
  CompactionError: ["compact", "probe", cause],
  BranchSummaryError: ["summary", "probe", cause],
};

for (const pkg of request.packages) {
  const report = {};
  out.packages[pkg.module] = report;
  let ns = null;
  if (pkg.file) {
    try {
      ns = await import(pathToFileURL(join(runtimeDir, pkg.file)).href);
    } catch (err) {
      ns = null;
    }
  }
  for (const exp of pkg.exports) {
    if (!ns || !(exp.name in ns)) {
      report[exp.name] = { status: "missing", members: {} };
      continue;
    }
    const value = ns[exp.name];
    const entry = { status: "bridged", reason: "", members: {} };
    report[exp.name] = entry;
    if (typeof value === "function") {
      entry.status = await origin(value);
      if (entry.status === "stand-in") entry.reason = standInReason(value, exp.kind === "class");
    } else if (value !== null && typeof value === "object") {
      entry.status = vendoredValues.has(value) ? "vendored" : "bridged";
    }
    if (exp.kind !== "class" || !exp.members?.length) continue;
    if (entry.status === "stand-in" || typeof value !== "function") {
      for (const m of exp.members) entry.members[m] = { status: entry.status === "stand-in" ? "stand-in" : "missing", reason: entry.reason };
      continue;
    }
    let instance = null;
    const source = classSources(value);
    if (value.prototype instanceof Error) {
      try {
        // Error constructors are probed with valid non-default inputs. Other
        // classes may own files, watchers or workers; never construct them to
        // inspect their surface.
        instance = source.includes("Object.assign(this, props)")
          ? new value(Object.fromEntries(exp.members.filter((m) => !["stack", "toJSON", "name", "_tag"].includes(m)).map((m) => [m, m === "cause" ? cause : "probe"])))
          : new value(...(errorProbeArgs[exp.name] ?? []));
      } catch {
        instance = null;
      }
    }
    for (const m of exp.members) {
      const key = memberKey(m, ns);
      const member = findMember(value, key);
      if (typeof member === "function") {
        const status = await origin(member);
        entry.members[m] = { status, reason: status === "stand-in" ? standInReason(member.bind(instance ?? {}), false) : "" };
        continue;
      }
      const fieldRE = new RegExp(`(this\\.${m.replace(/[^\w$]/g, "")}\\b|^\\s*(?:static\\s+)?${m.replace(/[^\w$]/g, "")}\\s*[=;])`, "m");
      const computedName = /^\[Symbol\.(\w+)\]$/.exec(m)?.[1];
      const computedField = typeof key === "symbol" && computedName &&
        new RegExp(`^\\s*\\[${computedName}\\]\\s*[=;]`, "m").test(source);
      if ((instance && key in instance) || member !== undefined || computedField || (typeof key === "string" && fieldRE.test(source))) {
        entry.members[m] = { status: entry.status, reason: "" };
      } else {
        entry.members[m] = { status: "missing", reason: "" };
      }
    }
  }
}

session.disconnect();
await writeFile(outputPath, JSON.stringify(out));
// Vendored modules may hold timers or handles open; the report is complete.
process.exit(0);
