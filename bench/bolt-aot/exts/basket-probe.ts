// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
// Allocation probe for bench/bolt-aot/basket.py: one command, /basket-probe [gc LABEL], that reports what the JS runtime hosting the extension exposes about its heap,
// as one "PROBE:{json}" notification. It reads the same things on Node, Bun and the compiled host, and names what a runtime does not expose instead of guessing:
//   - process.memoryUsage()                         all three (rss, heapTotal, heapUsed, external, arrayBuffers)
//   - v8.getHeapStatistics()                        Node: total_allocated_bytes is the cumulative allocation (Bun's node:v8 shim reports other, JSC-derived values; `runtime` says who answered)
//   - bun:jsc heapStats() and memoryUsage()         Bun and the compiled host (live object count, heap size, top object types)
//   - GC events seen by a PerformanceObserver       Node (count and total pause since the probe loaded); a runtime without gc entries leaves the count 0
// The reading `raw` is taken as the command arrives; with "gc LABEL" the probe then writes "BASKET-MARK LABEL" to stderr (in order with JSC's own GC log, which basket.py cuts into phases
// at the marks), forces a full collection (Node: --expose-gc switched on at run time; Bun and the compiled host: Bun.gc(true)) and takes the reading `live`.
// It imports nothing from Pi and reads no files, so every runtime loads it the same way and the compiled host compiles it in.
import v8 from "node:v8";
import vm from "node:vm";
import { PerformanceObserver } from "node:perf_hooks";

interface BunGlobal {
  gc(force: boolean): void;
}
interface JscModule {
  heapStats(): Record<string, unknown> & { objectTypeCounts?: Record<string, number> };
  memoryUsage?(): unknown;
}
interface ProbeApi {
  registerCommand(name: string, spec: { description: string; handler: (args: string, ctx: { ui: { notify(message: string, level: string): void } }) => Promise<void> }): void;
}

const gcEvents = { count: 0, pauseMs: 0 };
try {
  new PerformanceObserver((list) => {
    for (const entry of list.getEntries()) {
      gcEvents.count += 1;
      gcEvents.pauseMs += entry.duration;
    }
  }).observe({ entryTypes: ["gc"] });
} catch {
  // the runtime has no gc entries: the counters stay 0 and `runtime` tells the reader which runtime that is
}

function safe<T>(f: () => T): T | { error: string } {
  try {
    return f();
  } catch (e) {
    return { error: e instanceof Error ? e.message : String(e) };
  }
}

async function jsc(): Promise<unknown> {
  // Runtime-selected import: bun:jsc exists only on Bun and the compiled host, and the specifier is not a literal so Node's loader never resolves it.
  const spec = "bun:" + "jsc";
  const mod = (await import(spec)) as JscModule;
  const stats = mod.heapStats();
  const { objectTypeCounts, ...rest } = stats;
  const topTypes = Object.entries(objectTypeCounts ?? {})
    .sort((a, b) => b[1] - a[1])
    .slice(0, 8);
  return { heapStats: { ...rest, topTypes }, memoryUsage: mod.memoryUsage?.() };
}

// globalThis.Bun is the one place the probe tells the runtimes apart; the global is not in Node's typings, so the cast is to a type that says exactly that.
const bunGlobal = globalThis as unknown as { Bun?: BunGlobal };

export default function (pi: ProbeApi) {
  pi.registerCommand("basket-probe", {
    description: "Report the JS heap numbers the runtime exposes (benchmark probe)",
    handler: async (args, ctx) => {
      const bun = bunGlobal.Bun;
      const [mode, label] = args.trim().split(/\s+/);
      const out: Record<string, unknown> = { runtime: bun ? "bun-family" : "node", version: process.version, bunVersion: process.versions?.bun, gcForced: false };
      // raw: what the heap holds when the command arrives; live: after a full collection when asked for one ("gc LABEL"), else the same moment again
      out.raw = { memoryUsage: safe(() => process.memoryUsage()), v8: safe(() => v8.getHeapStatistics()), gcEvents: { ...gcEvents } };
      if (mode === "gc") {
        // a marker on stderr, in order with the engine's own GC log, so basket.py can cut the log into phases
        process.stderr.write("BASKET-MARK " + (label ?? "") + "\n");
        if (bun) {
          bun.gc(true);
        } else {
          v8.setFlagsFromString("--expose-gc");
          const gc: unknown = vm.runInNewContext("gc");
          if (typeof gc === "function") gc();
        }
        out.gcForced = true;
      }
      out.live = { memoryUsage: safe(() => process.memoryUsage()), v8: safe(() => v8.getHeapStatistics()), gcEvents: { ...gcEvents } };
      if (bun) out.jsc = await jsc().catch((e: unknown) => ({ error: e instanceof Error ? e.message : String(e) }));
      ctx.ui.notify("PROBE:" + JSON.stringify(out), "info");
    },
  });
}
