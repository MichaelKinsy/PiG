// The TinyGo escape-analysis baseline of the Durable core: how many allocation sites each core package has that TinyGo puts on the
// heap (the `-print-allocs` report of build-wasm.sh: "object allocated on the heap: <reason>"). A new site in the step path is a new
// allocation per step, so the count of a package may only fall:
//
//   node budget/allocs.mjs --check      fail when a package has more sites than allocs.json, or one the file lacks; ask for a lower
//                                       baseline when a package has fewer
//   node budget/allocs.mjs --update     rewrite allocs.json from a build
//
// The count is a static property of the source and the TinyGo version, so it repeats exactly. Sites of the standard library that
// the core's generic instantiations pull in are counted under "stdlib".
import { spawnSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, relative } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const core = join(here, "..")
const baselinePath = join(here, "allocs.json")

/** Build cmd/corewasm with -print-allocs over the core's packages and return the sites per package ("." is the core itself). */
export function measure() {
  const out = mkdtempSync(join(tmpdir(), "allocs-"))
  try {
    const r = spawnSync("sh", ["budget/build-wasm.sh", "tinygo", "cmd/corewasm", join(out, "core.wasm")], {
      cwd: core, encoding: "utf8", maxBuffer: 1 << 28, env: { ...process.env, CORE_PRINT_ALLOCS: "PiG/durable/core" },
    })
    if (r.status !== 0) throw new Error(`the TinyGo build failed (${r.status}):\n${(r.stdout + r.stderr).slice(-2000)}`)
    return sitesOf(`${r.stdout}\n${r.stderr}`)
  } finally {
    rmSync(out, { recursive: true, force: true })
  }
}

/** The sites per package of one build's output; the report is on the compiler's stderr. */
export function sitesOf(text) {
  const sites = {}
  for (const line of text.split("\n")) {
    const m = /^([^:]+)(?::\d+:\d+)?: object allocated on the heap/.exec(line)
    if (!m) continue
    const rel = relative(core, dirname(m[1]))
    const pkg = rel === "" ? "." : rel.startsWith("..") ? "stdlib" : rel
    sites[pkg] = (sites[pkg] ?? 0) + 1
  }
  return Object.fromEntries(Object.entries(sites).sort(([a], [b]) => (a < b ? -1 : 1)))
}

/** The measured sites against the baseline: "higher" fails, "lower" asks for a lower baseline. */
export function compare(baseline, measured) {
  const higher = [], lower = []
  for (const pkg of new Set([...Object.keys(baseline), ...Object.keys(measured)])) {
    const was = baseline[pkg], now = measured[pkg] ?? 0
    if (was === undefined) higher.push(`${pkg}: ${now} sites, the baseline has no such package`)
    else if (now > was) higher.push(`${pkg}: ${now} sites > ${was}`)
    else if (now < was) lower.push(`${pkg}: ${now} sites < ${was}: lower the baseline (--update)`)
  }
  return { higher, lower }
}

const total = sites => Object.values(sites).reduce((a, b) => a + b, 0)

function run() {
  const mode = process.argv[2]
  if (mode !== "--check" && mode !== "--update") {
    console.error("usage: allocs.mjs --check | --update")
    process.exit(2)
  }
  const measured = measure()
  if (mode === "--update") {
    writeFileSync(baselinePath, `${JSON.stringify(measured, null, "\t")}\n`)
    console.log(`allocs: wrote ${baselinePath}: ${total(measured)} sites in ${Object.keys(measured).length} packages`)
    return
  }
  const { higher, lower } = compare(JSON.parse(readFileSync(baselinePath, "utf8")), measured)
  for (const l of [...higher, ...lower]) console.error(`FAIL ${l}`)
  if (higher.length + lower.length > 0) process.exit(1)
  console.log(`allocs: ${total(measured)} sites in ${Object.keys(measured).length} packages, equal to allocs.json`)
}

if (process.argv[1] === fileURLToPath(import.meta.url)) run()
