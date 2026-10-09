// Size, startup and bundle budgets of the Durable core (ADR D15, docs/plan/durable-core RISKS R6).
//
//   node durable/core/budget/measure.mjs [--core <main package dir>] [--toolchains go,tinygo] [--check] [--json out.json]
//
// For each toolchain it builds the core main package, then reports raw and compressed size, the WASI imports against the ABI
// table, and the median time to compile, instantiate and initialize the module. It also bundles the Cloudflare host and reports
// its minified and gzip size. With --check it exits 1 when a figure passes its limit in budget.json. The production core is
// durable/core/wasm; until it exists the probe core (durable/core/probe/wasm) is measured against the "probe" limits, so
// the script and its gates are proven before the core lands.
import { execFileSync } from "node:child_process"
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { brotliCompressSync, constants, gzipSync } from "node:zlib"

const here = dirname(fileURLToPath(import.meta.url))
const repo = resolve(here, "..", "..", "..")
const args = process.argv.slice(2)
const flag = name => args.includes(`--${name}`)
const value = name => { const i = args.indexOf(`--${name}`); return i >= 0 ? args[i + 1] : undefined }

const production = join(repo, "durable", "core", "wasm")
const probe = join(repo, "durable", "core", "probe", "wasm")
const coreDir = resolve(value("core") ?? (existsSync(join(production, "main.go")) ? production : probe))
const limitsKey = coreDir === probe ? "probe" : "core"
const toolchains = (value("toolchains") ?? "go,tinygo").split(",")
const budget = JSON.parse(readFileSync(join(here, "budget.json"), "utf8"))
const abi = JSON.parse(readFileSync(join(repo, "durable", "core", "abi", "abi.json"), "utf8"))
const allowedImports = new Set(abi.imports.map(i => i.name))
const required = abi.exports.map(e => e.name).filter(n => n !== "_initialize")

const median = xs => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)]
const mib = n => (n / 1048576).toFixed(2)

/** Compile, instantiate and initialize through the production loader, the way a Worker does it at startup. */
async function instantiateMs(bytes, runs = 7) {
  const { WasmCore } = await import(join(repo, "durable", "core", "host", "cf", "src", "wasm.ts"))
  const times = []
  for (let i = 0; i < runs; i++) {
    const t0 = performance.now()
    const module = new WebAssembly.Module(bytes)
    new WasmCore(module, { log: () => {} })
    times.push(performance.now() - t0)
  }
  return median(times)
}

const outDir = mkdtempSync(join(tmpdir(), "durable-core-budget-"))
const report = { core: coreDir.replace(`${repo}/`, ""), limits: limitsKey, toolchains: {}, host: {}, failures: [] }
const fail = message => report.failures.push(message)

for (const toolchain of toolchains) {
  const out = join(outDir, `core-${toolchain}.wasm`)
  try {
    execFileSync(join(here, "build-wasm.sh"), [toolchain, coreDir, out], { stdio: ["ignore", "inherit", "inherit"], env: process.env })
  } catch (e) {
    fail(`${toolchain}: the build failed (${e.status ?? e.message})`)
    continue
  }
  const bytes = readFileSync(out)
  const module = new WebAssembly.Module(bytes)
  const imports = WebAssembly.Module.imports(module).map(i => `${i.module}.${i.name}`)
  const exports = new Set(WebAssembly.Module.exports(module).map(e => e.name))
  const outside = imports.filter(i => !allowedImports.has(i))
  const missing = required.filter(e => !exports.has(e))
  const figures = {
    rawBytes: bytes.length,
    gzipBytes: gzipSync(bytes, { level: 9 }).length,
    brotliBytes: brotliCompressSync(bytes, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }).length,
    instantiateMs: Math.round((await instantiateMs(bytes)) * 10) / 10,
    imports: imports.length,
  }
  report.toolchains[toolchain] = figures
  if (outside.length) fail(`${toolchain}: imports outside the ABI table: ${outside.join(", ")}`)
  if (missing.length) fail(`${toolchain}: missing ABI exports: ${missing.join(", ")}`)
  const limits = budget[limitsKey][toolchain]
  for (const key of Object.keys(limits)) if (figures[key] > limits[key]) fail(`${toolchain}: ${key} ${figures[key]} is over the ${limitsKey} budget ${limits[key]}`)
  if (figures.rawBytes > budget.workerHardLimits.uncompressedBytes) fail(`${toolchain}: the module alone passes the Worker size limit`)
  if (figures.instantiateMs > budget.workerHardLimits.startupMs) fail(`${toolchain}: startup passes the Worker limit of ${budget.workerHardLimits.startupMs} ms`)
}

// The JavaScript host a Worker ships, without pi-ai and pi-durable's declarations (the application's own dependencies).
try {
  const { build } = await import(join(repo, "durable", "core", "host", "cf", "node_modules", "esbuild", "lib", "main.js"))
  const result = await build({
    entryPoints: [join(repo, "durable", "core", "host", "cf", "src", "index.ts")], bundle: true, minify: true, write: false, format: "esm", platform: "neutral",
    target: "es2024", conditions: ["workerd", "worker", "browser", "import"], mainFields: ["module", "main"], logLevel: "error",
    external: ["cloudflare:*", "node:*", "@earendil-works/pi-ai", "@earendil-works/pi-ai/*", "@earendil-works/pi-durable", "@earendil-works/pi-durable/*"],
  })
  const js = result.outputFiles[0].contents
  report.host = { minifiedBytes: js.length, gzipBytes: gzipSync(js, { level: 9 }).length }
  for (const key of Object.keys(budget.host)) if (report.host[key] > budget.host[key]) fail(`host bundle: ${key} ${report.host[key]} is over the budget ${budget.host[key]}`)
} catch (e) {
  fail(`host bundle: ${e.message}`)
}

const lines = [`core: ${report.core} (limits: ${limitsKey})`, "", "| toolchain | raw MiB | gzip MiB | brotli MiB | instantiate ms | imports |", "|---|---:|---:|---:|---:|---:|"]
for (const [name, f] of Object.entries(report.toolchains)) lines.push(`| ${name} | ${mib(f.rawBytes)} | ${mib(f.gzipBytes)} | ${mib(f.brotliBytes)} | ${f.instantiateMs} | ${f.imports} |`)
if (report.host.minifiedBytes) lines.push("", `host bundle: ${(report.host.minifiedBytes / 1024).toFixed(1)} KiB minified, ${(report.host.gzipBytes / 1024).toFixed(1)} KiB gzip`)
console.log(lines.join("\n"))
if (value("json")) writeFileSync(value("json"), JSON.stringify(report, null, 2) + "\n")
if (report.failures.length) {
  console.error(`\n${report.failures.length} budget failure(s):\n- ${report.failures.join("\n- ")}`)
  if (flag("check")) process.exit(1)
}
