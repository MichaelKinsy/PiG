// Several Durable Objects in one isolate (robust-durable item 4): OBJECTS objects of one target, each with its own copy of the
// SIZE-turn fixture, in one Miniflare (one workerd process, one isolate for the worker). Each object is woken, then every object
// runs TURNS warm turns, round robin. After every request the isolate's memory is read without a collection (V8 heap used +
// ArrayBuffer backing store + the linear memory of every Wasm core instance in the isolate); after the last turn a forced
// collection gives what the isolate retains. Cloudflare's limit is 128 MB per isolate, JS heap and Wasm together.
//   BENCH_PROBE=count bun bench/isolate.ts <target> <size> [objects=4] [turns=10]
// Appends one line to results/isolate.jsonl: {kind:"isolate", target, version, turns, objects, warm, peak, retained, ...}.
import { execFileSync } from "node:child_process"
import { appendFileSync, copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { MEASURED_TOOLS, turn } from "../src/plan.ts"
import { call, fixture, PROBE, prepare, start, target, TARGETS } from "./targets.ts"

if (PROBE !== "count") throw new Error("set BENCH_PROBE=count")
const name = target(process.argv[2])
const size = Number(process.argv[3])
const objects = Number(process.argv[4] ?? 4)
const turns = Number(process.argv[5] ?? 10)
const MB = 1e6

type Heap = { usedSize: number; totalSize: number; backingStorageSize: number }
async function inspector(url: URL) {
  const list = await (await fetch(`http://${url.host}/json`)).json() as { title: string; webSocketDebuggerUrl: string }[]
  const user = list.find(t => t.title.startsWith("workerd: worker core:user:"))
  if (!user) throw new Error(`isolate: no user worker among ${list.map(t => t.title).join(", ")}`)
  const ws = new WebSocket(user.webSocketDebuggerUrl)
  await new Promise((ok, fail) => { ws.onopen = ok; ws.onerror = fail })
  let id = 0
  const send = <T>(method: string) => new Promise<T>(ok => {
    const want = ++id
    ws.onmessage = event => { const m = JSON.parse(String(event.data)); if (m.id === want) ok(m.result) }
    ws.send(JSON.stringify({ id: want, method }))
  })
  // workerd answers HeapProfiler.takeHeapSnapshot (it collects garbage first) but not collectGarbage.
  return { heap: () => send<Heap>("Runtime.getHeapUsage"), gc: () => send<unknown>("HeapProfiler.takeHeapSnapshot"), close: () => ws.close() }
}

// The object databases: a first Miniflare creates each object's file, then every file is replaced by the fixture's database.
await prepare(name)
const dir = mkdtempSync(join(tmpdir(), "durable-bench-isolate-"))
const names = Array.from({ length: objects }, (_, i) => `o${i + 1}`)
{
  const mf = await start(name, dir)
  for (const o of names) await call(mf, `/stats?o=${o}`)
  await mf.dispose()
}
const objectDir = join(dir, "-PiDO")
const files = readdirSync(objectDir).filter(f => f.endsWith(".sqlite") && f !== "metadata.sqlite")
if (files.length !== objects) throw new Error(`isolate: ${files.length} object databases, want ${objects}`)
const source = join(fixture(name, size), "-PiDO")
const fixtureDb = readdirSync(source).find(f => f.endsWith(".sqlite") && f !== "metadata.sqlite")!
for (const f of files) for (const suffix of ["", "-wal", "-shm"]) {
  rmSync(join(objectDir, f + suffix), { force: true })
  if (existsSync(join(source, fixtureDb + suffix))) copyFileSync(join(source, fixtureDb + suffix), join(objectDir, f + suffix))
}

const mf = await start(name, dir, { inspector: true })
const insp = await inspector(new URL(String(await mf.getInspectorURL())))
const workerd = () => {
  const rows = execFileSync("ps", ["-A", "-o", "pid=,ppid=,comm="], { encoding: "utf8" }).trim().split("\n").map(line => line.trim().split(/\s+/))
  const tree = new Set([process.pid])
  for (let grew = true; grew;) { grew = false; for (const [pid, ppid] of rows) if (tree.has(Number(ppid)) && !tree.has(Number(pid))) { tree.add(Number(pid)); grew = true } }
  return rows.filter(([pid, , comm]) => tree.has(Number(pid)) && comm === "workerd").map(([pid]) => Number(pid))
}
const pids = workerd()
let peak = { total: 0, heapUsed: 0, backing: 0, wasmBytes: 0, at: "" }
const latency: Record<string, number[]> = { open: [], turn: [] }
const reading = async (at: string) => {
  const heap = await insp.heap()
  const { wasmBytes } = await call<{ wasmBytes: number }>(mf, `/probe?o=${names[0]}`)
  const total = heap.usedSize + heap.backingStorageSize + wasmBytes
  if (total > peak.total) peak = { total, heapUsed: heap.usedSize, backing: heap.backingStorageSize, wasmBytes, at }
  return total
}
for (const o of names) {
  const t0 = performance.now()
  await call(mf, `/wake?o=${o}`)
  latency.open!.push(performance.now() - t0)
  await reading(`open ${o}`)
}
for (let t = 0; t < turns; t++) for (const o of names) {
  const t0 = performance.now()
  await call(mf, `/turn?o=${o}`, turn(`m${t}`, MEASURED_TOOLS))
  latency.turn!.push(performance.now() - t0)
  await reading(`turn m${t} ${o}`)
}
await insp.gc()
const after = await insp.heap()
const { wasmBytes } = await call<{ wasmBytes: number }>(mf, `/probe?o=${names[0]}`)
const retained = { total: after.usedSize + after.backingStorageSize + wasmBytes, heapUsed: after.usedSize, backing: after.backingStorageSize, wasmBytes }
const stats = await Promise.all(names.map(o => call<{ tables: Record<string, number> }>(mf, `/stats?o=${o}`)))
insp.close()
await mf.dispose()
rmSync(dir, { recursive: true, force: true })
const line = { kind: "isolate", target: name, version: TARGETS[name]!.version, turns: size, objects, warmTurns: turns, peak, retained, limit: 128 * MB, latency, entries: stats.map(s => s.tables.entries), pids: pids.length, at: new Date().toISOString() }
mkdirSync("results", { recursive: true })
appendFileSync("results/isolate.jsonl", JSON.stringify(line) + "\n")
process.stderr.write(`isolate ${name} ${size} x${objects}: peak ${(peak.total / MB).toFixed(1)} MB (${peak.at}), retained ${(retained.total / MB).toFixed(1)} MB (heap ${(retained.heapUsed / MB).toFixed(1)}, backing ${(retained.backing / MB).toFixed(1)}, wasm ${(retained.wasmBytes / MB).toFixed(1)}) of 128 MB\n`)
