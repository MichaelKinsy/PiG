// A Durable Object over the probe core, for the Miniflare tests: it exposes what the tests observe through RPC.
import { DurableObject } from "cloudflare:workers"
import { EVENT, NOTICE } from "../../src/abi.ts"
import { CorePool } from "../../src/pool.ts"
import { DoSession } from "../../src/do-session.ts"
import { HostRegistry } from "../../src/registry.ts"
// @ts-expect-error the test runner supplies the compiled module
import wasm from "./core.wasm"

const pool = new CorePool(wasm, { highWater: 8 * 1024 * 1024 })
const OPEN = { settings: {}, agent: { provider: "probe", modelId: "probe-1" }, models: [], registry: [], sidecar: "index", delta: false }
type Env = { PI: DurableObjectNamespace }
const dec = new TextDecoder()

export class ProbeDO extends DurableObject<Env> {
  private session?: DoSession
  private notices: string[] = []
  private fatal: string[] = []
  private coldOpensBefore = 0

  private s(): DoSession {
    return this.session ??= new DoSession({
      pool,
      storage: this.ctx.storage,
      open: () => OPEN,
      runtime: () => ({
        registry: new HostRegistry({ snapshot: () => ({ installed: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} }),
        models: {
          getModel: () => ({}),
          // A stream that stays open until the host aborts it: the probe never completes a model call by itself.
          streamSimple: (_model: unknown, _context: unknown, options: { signal: AbortSignal }) => ({
            async *[Symbol.asyncIterator]() { await new Promise<void>(resolve => options.signal.addEventListener("abort", () => resolve())) },
            result: async () => ({}),
          }),
        },
        report: () => {},
      }),
      onNotice: n => { this.notices.push(`${n.kind}:${dec.decode(n.payload).slice(0, 80)}`) },
      onFatal: e => { this.fatal.push(e.message) },
    })
  }

  wake() { this.s().ensureOpen(); return { coldOpens: this.s().coldOpens, notices: this.notices.splice(0) } }

  submit(content: string, requestId: string) {
    const host = this.s().ensureOpen()
    const step = host.sendJSON(EVENT.submit, { type: "input", content, requestId })
    const timers = this.s().timers
    return { status: step?.status, effects: step?.effects.length, notices: this.notices.splice(0), durable: timers?.durablePending, volatile: timers?.volatilePending, alarmWrites: timers?.alarmWrites, alarmAt: timers?.alarmAt }
  }

  fireDue() { this.s().alarm(); return this.notices.splice(0) }

  async alarm() { this.s().alarm() }

  async alarmAt() { return await this.ctx.storage.getAlarm() }

  unload() { this.s().unload(); return { open: this.s().open } }

  /** Every notice delivered since the last call. */
  drain() { return this.notices.splice(0) }

  state() {
    const s = this.s()
    return { open: s.open, coldOpens: s.coldOpens, alarmWrites: s.timers?.alarmWrites ?? 0, durable: s.timers?.durablePending ?? 0, volatile: s.timers?.volatilePending ?? 0, fatal: this.fatal, notices: this.notices.length, pool: { created: pool.created, recycled: pool.recycled, instances: pool.instances } }
  }

  rows() {
    return this.ctx.storage.sql.exec("SELECT n, note FROM probe_log ORDER BY n").toArray().map(r => `${r.n}:${r.note}`)
  }
}

export default { fetch: async (request: Request, env: Env) => {
  const url = new URL(request.url)
  const stub = env.PI.getByName(url.searchParams.get("name") ?? "main") as unknown as ProbeDO
  const arg = request.method === "POST" ? await request.json() as any : {}
  switch (url.pathname) {
    case "/wake": return Response.json(await stub.wake())
    case "/submit": return Response.json(await stub.submit(arg.content, arg.requestId))
    case "/fire": return Response.json(await stub.fireDue())
    case "/alarm": return Response.json(await stub.alarmAt())
    case "/unload": return Response.json(await stub.unload())
    case "/drain": return Response.json(await stub.drain())
    case "/state": return Response.json(await stub.state())
    case "/rows": return Response.json(await stub.rows())
    default: return new Response("not found", { status: 404 })
  }
} }
void NOTICE
