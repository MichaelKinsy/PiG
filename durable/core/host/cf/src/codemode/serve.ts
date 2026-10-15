// The remote end of the codemode sandbox on a Worker (Pi's serveCodemodeRemote, packages/codemode/src/runtime/remote.ts): one
// execution's VM runs here and everything it reports goes out through `exchange`.
//
// The VM driver is the codemode Go module (TinyGo, -scheduler=none: no goroutines and only synchronous imports), which runs
// QuickJS through quickjs.ts. Pi's exchange is asynchronous, so the asynchrony stays here: the module is a step machine,
// each of its exports takes one event (the request, a reply, a rejected exchange) and returns the exchanges to send, and
// this driver awaits them and re-enters with the answers. A new pair of instances serves each execution and is dropped
// when it ends, after an out-of-memory or a trap, so no linear memory outlives the execution.
import { goWasiImports } from "./gowasi.ts"
import type { CodemodeRemoteExchange, CodemodeRemoteMessage, CodemodeRemoteReply, CodemodeRemoteRequest } from "./protocol.ts"
import { QuickJSInstance } from "./quickjs.ts"

/** The two compiled modules a Worker bundles (Wrangler's `CompiledWasm` rule): quickjs-wasi's quickjs.wasm and the codemode module. */
export interface CodemodeModules {
  quickjs: WebAssembly.Module
  codemode: WebAssembly.Module
}

export interface CodemodeServeOptions {
  modules: CodemodeModules
  /** Upper bound for a request's interrupt budget, and the budget when a request names none. Default: the module's DefaultInterruptBudget. */
  maxInterruptBudget?: number
  /** Upper bound for a request's VM memory, and the limit when a request names none. */
  maxMemoryLimitBytes?: number
}

/** What the module returns after each event. */
interface Step {
  /** Exchanges to send, in order; the module names each one so the driver can answer it. */
  exchanges: { id: number; messages: CodemodeRemoteMessage[] }[]
  /** The execution is over: the terminal message was delivered or the client asked to stop. */
  resolved: boolean
}

const enc = new TextEncoder()
const dec = new TextDecoder()

/**
 * The output items the module has handed over (`host.output`, one JSON text per message) and no exchange has taken yet. They live here,
 * not in the module's memory, so a trap of the module loses none of them: it ends the execution with a crash message that follows them,
 * as Pi's serveCodemodeRemote does with the output it buffered.
 */
class HeldOutput {
  private readonly texts: string[] = []
  add(text: string): void { this.texts.push(text) }
  /** The first `count` items as messages. */
  take(count: number): CodemodeRemoteMessage[] {
    return count > 0 ? JSON.parse(`[${this.texts.splice(0, count).join(",")}]`) : []
  }
  takeAll(): CodemodeRemoteMessage[] { return this.take(this.texts.length) }
}

const EXPORTS = ["memory", "_initialize", "codemode_alloc", "codemode_serve_start", "codemode_serve_reply", "codemode_serve_failed", "codemode_out_count", "codemode_out_ptr", "codemode_out_len", "codemode_out_resolved", "codemode_host_call", "codemode_host_interrupt"]

/** One execution's pair of instances. */
class CodemodeInstance {
  private readonly go: Record<string, any>
  private readonly memory: WebAssembly.Memory

  private readonly held: HeldOutput

  constructor(modules: CodemodeModules, held: HeldOutput) {
    this.held = held
    const foreign = WebAssembly.Module.imports(modules.codemode).filter(i => i.module !== "wasi_snapshot_preview1" && i.module !== "qjs" && i.module !== "host")
    if (foreign.length) throw new Error(`the codemode module imports ${foreign.map(i => `${i.module}.${i.name}`).join(", ")}, which this host does not provide`)
    let go!: Record<string, any>
    const quickjs = new QuickJSInstance(modules.quickjs, {
      hostCall: (namePtr, nameLen, thisHandle, argc, argvPtr) => go.codemode_host_call(namePtr, nameLen, thisHandle, argc, argvPtr) as number,
      interrupt: () => go.codemode_host_interrupt() !== 0,
    })
    let memory!: WebAssembly.Memory
    const qjs = {
      call: (fn: number, argc: number, a0: bigint, a1: bigint, a2: bigint, a3: bigint) => quickjs.call(fn, argc, a0, a1, a2, a3),
      mem_read: (ptr: number, n: number, dst: number) => quickjs.readInto(ptr, new Uint8Array(memory.buffer, dst, n)),
      mem_write: (ptr: number, src: number, n: number) => quickjs.write(ptr, new Uint8Array(memory.buffer, src, n)),
      set_clock: (realtime: bigint, monotonic: bigint, virtual: number) => { if (virtual) quickjs.setClock(realtime, monotonic) },
      set_random_seed: (seed: number) => quickjs.setRandomSeed(seed),
    }
    const instance = new WebAssembly.Instance(modules.codemode, { wasi_snapshot_preview1: goWasiImports(() => memory), qjs, host: { output: (ptr: number, n: number) => held.add(dec.decode(new Uint8Array(memory.buffer, ptr, n))) } })
    go = instance.exports as Record<string, any>
    for (const name of EXPORTS) if (!(name in go)) throw new Error(`the codemode module does not export ${name}`)
    memory = go.memory as WebAssembly.Memory
    this.go = go
    this.memory = memory
    ;(go._initialize as () => void)()
  }

  start(input: unknown): Step { return this.step(() => this.go.codemode_serve_start(...this.put(input))) }
  reply(id: number, reply: CodemodeRemoteReply): Step { return this.step(() => this.go.codemode_serve_reply(id, ...this.put(reply))) }
  failed(id: number): Step { return this.step(() => this.go.codemode_serve_failed(id)) }

  private put(value: unknown): [number, number] {
    const bytes = enc.encode(JSON.stringify(value))
    const ptr = this.go.codemode_alloc(bytes.length) as number
    new Uint8Array(this.memory.buffer, ptr, bytes.length).set(bytes)
    return [ptr, bytes.length]
  }

  /** Runs one event and reads what it produced: one JSON text per exchange, so no script output is joined into one buffer. */
  private step(call: () => void): Step {
    call()
    const exchanges: Step["exchanges"] = []
    const count = this.go.codemode_out_count() as number
    for (let i = 0; i < count; i++) {
      const ptr = this.go.codemode_out_ptr(i) as number, length = this.go.codemode_out_len(i) as number
      const chunk = JSON.parse(dec.decode(new Uint8Array(this.memory.buffer, ptr, length))) as { id: number; take: number; messages: CodemodeRemoteMessage[] }
      exchanges.push({ id: chunk.id, messages: [...this.held.take(chunk.take), ...chunk.messages] })
    }
    return { exchanges, resolved: (this.go.codemode_out_resolved() as number) !== 0 }
  }
}

/**
 * Runs one execution's VM here and forwards everything it reports through `exchange`. Resolves once the VM has stopped and the
 * terminal message has been delivered, or the client asked to stop. Never rejects.
 */
export function serveCodemodeRemote(request: CodemodeRemoteRequest, exchange: CodemodeRemoteExchange, options: CodemodeServeOptions): Promise<void> {
  return new Promise<void>(resolve => {
    let instance: CodemodeInstance | undefined
    const held = new HeldOutput()
    let over = false
    const finish = () => {
      over = true
      instance = undefined
      resolve()
    }
    /**
     * Ends the execution with a terminal message the module could not send itself, and resolves once the client has answered
     * that exchange or rejected it (Pi's serveCodemodeRemote `end`). The instances are dropped at once.
     */
    const end = (terminal: CodemodeRemoteMessage) => {
      if (over) return
      over = true
      instance = undefined
      let delivered: Promise<unknown>
      try { delivered = Promise.resolve(exchange([...held.takeAll(), terminal])) } catch { delivered = Promise.resolve() }
      void delivered.catch(() => null).then(() => resolve())
    }
    const apply = (run: (vm: CodemodeInstance) => Step) => {
      if (over || !instance) return
      let step: Step
      // A trap is what Pi's VM reports as a crash (runtime/vm.ts crashMessage).
      try { step = run(instance) } catch (error) { end({ type: "crash", message: error instanceof Error ? `${error.name}: ${error.message}` : String(error) }); return }
      for (const { id, messages } of step.exchanges) send(id, messages)
      if (step.resolved) finish()
    }
    const send = (id: number, messages: CodemodeRemoteMessage[]) => {
      let sent: Promise<CodemodeRemoteReply>
      try { sent = Promise.resolve(exchange(messages)) } catch (error) { sent = Promise.reject(error) }
      sent.then(
        reply => apply(vm => vm.reply(id, reply)),
        () => apply(vm => vm.failed(id)),
      )
    }

    // An instance that cannot be made is a launch failure (Pi's serveCodemodeRemote: the launcher rejected).
    try { instance = new CodemodeInstance(options.modules, held) } catch (error) {
      end({ type: "failure", error: { kind: "sandbox", message: error instanceof Error ? error.message : String(error) } })
      return
    }
    apply(vm => vm.start({ request, options: { maxInterruptBudget: options.maxInterruptBudget, maxMemoryLimitBytes: options.maxMemoryLimitBytes } }))
  })
}
