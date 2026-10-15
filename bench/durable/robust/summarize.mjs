// One line per durable-bench probe record: open, first and warm-p50 turn (wall, CPU, core time), rows, crossings, retained memory.
//   node summarize.mjs <probe.jsonl>...
import { readFileSync } from "node:fs"
const med = a => { const s = [...a].sort((x, y) => x - y); return s[Math.floor(s.length / 2)] }
for (const f of process.argv.slice(2)) {
  for (const line of readFileSync(f, "utf8").trim().split("\n").map(JSON.parse)) {
    if (line.kind !== "probe") continue
    const warm = line.turn.slice(1)
    const r = line.retained
    console.log(`${line.mode.padEnd(5)} ${line.target.padEnd(10)} ${String(line.turns).padStart(5)}  open ${line.open.ms.toFixed(0)}ms cpu ${line.open.cpuMs.toFixed(0)}ms | first ${line.turn[0].ms.toFixed(0)}ms | warm p50 ${med(warm.map(t => t.ms)).toFixed(0)}ms cpu ${med(warm.map(t => t.cpuMs)).toFixed(0)}ms core ${med(warm.map(t => t.coreMs)).toFixed(0)}ms rowsR ${med(warm.map(t => t.rowsRead))} rowsW ${med(warm.map(t => t.rowsWritten))} cross ${med(warm.map(t => t.crossings))} | wasm ${(r.wasmBytes / 1048576).toFixed(0)}MiB retained ${((r.heapUsed + r.backing + r.wasmBytes) / 1e6).toFixed(1)}MB`)
  }
}
