// Bundling a target Worker with esbuild and starting it in Miniflare (the same compatibility date and flags as durable-bench).
import { copyFileSync, mkdirSync } from "node:fs"
import { builtinModules } from "node:module"
import { basename, dirname, join, resolve } from "node:path"
import { build } from "esbuild"
import { Miniflare } from "miniflare"
import type { Readable } from "node:stream"
import { createInterface } from "node:readline"

export const COMPATIBILITY_DATE = "2026-07-30"
export const COMPATIBILITY_FLAGS = ["nodejs_compat"]

export interface WorkerSpec {
  /** TypeScript entry of the Worker. */
  entry: string
  /** Output directory for the bundle and its modules. */
  outDir: string
  /** Durable Object bindings: binding name to class name. */
  objects: Record<string, string>
  /** A compiled Wasm module the entry imports as ./core.wasm. */
  wasm?: string
  /** Plain-text bindings (SCRIPT, SIDECAR, ...). */
  bindings?: Record<string, string>
  /** esbuild `define` entries. */
  define?: Record<string, string>
}

const bundles = new Map<string, Promise<string>>()

/** Bundle the entry once per (entry, defines, outDir). */
export function bundle(spec: WorkerSpec): Promise<string> {
  const key = JSON.stringify([spec.entry, spec.outDir, spec.define])
  return bundles.get(key) ?? (bundles.set(key, (async () => {
    mkdirSync(spec.outDir, { recursive: true })
    const outfile = join(spec.outDir, `${basename(spec.entry).replace(/\.ts$/, "")}.mjs`)
    await build({
      entryPoints: [resolve(spec.entry)], outfile, bundle: true, format: "esm", platform: "neutral", target: "es2024",
      conditions: ["workerd", "worker", "browser", "import"], mainFields: ["module", "main"], define: spec.define ?? {},
      external: ["cloudflare:*", "node:*", "./core.wasm", ...builtinModules], logLevel: "error",
    })
    return outfile
  })()), bundles.get(key)!)
}

/** Start the Worker over a persisted Durable Object directory. */
export async function startWorker(spec: WorkerSpec, persist: string): Promise<Miniflare> {
  const outfile = await bundle(spec)
  // workerd resolves every module under modulesRoot; the Wasm module must sit next to the bundle as ./core.wasm.
  if (spec.wasm) copyFileSync(spec.wasm, join(dirname(outfile), "core.wasm"))
  const mf = new Miniflare({
    modulesRoot: dirname(resolve(outfile)),
    modules: [{ type: "ESModule", path: resolve(outfile) }, ...(spec.wasm ? [{ type: "CompiledWasm" as const, path: resolve(join(dirname(outfile), "core.wasm")) }] : [])],
    compatibilityDate: COMPATIBILITY_DATE,
    compatibilityFlags: COMPATIBILITY_FLAGS,
    bindings: spec.bindings ?? {},
    durableObjects: Object.fromEntries(Object.entries(spec.objects).map(([binding, className]) => [binding, { className, useSQLite: true }])),
    durableObjectsPersist: persist,
    handleRuntimeStdio: (stdout: Readable, stderr: Readable) => {
      stdout.on("data", chunk => process.stdout.write(chunk))
      createInterface({ input: stderr }).on("line", line => { if (!line.includes("NOSENTRY")) process.stderr.write(`${line}\n`) })
    },
  })
  try {
    await withTimeout(mf.ready, START_TIMEOUT_MS, "Miniflare did not become ready")
  } catch (e) {
    await mf.dispose().catch(() => {})
    throw e
  }
  return mf
}

/** A runtime that fails to start must fail the run, not hang it: the 2026-10 hang awaited a dead workerd. */
export const START_TIMEOUT_MS = 60_000
export const CALL_TIMEOUT_MS = 120_000

export function withTimeout<T>(promise: Promise<T>, ms: number, what: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout>
  const timeout = new Promise<never>((_, reject) => { timer = setTimeout(() => reject(new Error(`${what} after ${ms} ms`)), ms) })
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer))
}

export async function call<T = unknown>(mf: Miniflare, path: string, body?: unknown): Promise<T> {
  const response = await withTimeout(mf.dispatchFetch(`http://bench${path}`, body === undefined ? {} : { method: "POST", body: JSON.stringify(body) }), CALL_TIMEOUT_MS, `${path} did not answer`)
  if (!response.ok) throw new Error(`${path} ${response.status}: ${await response.text()}`)
  return await response.json() as T
}
