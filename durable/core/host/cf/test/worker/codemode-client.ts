// The client end of the remote codemode protocol, for the Worker tests: the host role of Pi's RemoteCodemodeSandbox
// (packages/codemode/src/runtime/remote.ts remoteLauncher plus execution.ts), reduced to what the tests observe. Tools run here;
// the remote only runs the VM. The wire is the protocol of src/codemode/protocol.ts.
import type { CodemodeError, CodemodeOutputItem, CodemodeRemote, CodemodeRemoteExchange, CodemodeRemoteMessage, CodemodeRemoteReply, CodemodeRemoteRequest, VmStartData } from "../../src/codemode/protocol.ts"

export type Tool = (args: unknown, context: { signal: AbortSignal }) => unknown

export interface RunOptions {
  tools?: Record<string, Tool>
  timeoutMs?: number
  interruptBudget?: number
  memoryLimitBytes?: number
  /** Aborts the execution when it fires. */
  signal?: AbortSignal
  store?: Record<string, string>
}

export interface RunResult {
  result: { ok: true; value: unknown } | { ok: false; error: CodemodeError }
  output: CodemodeOutputItem[]
  calls: { name: string; status: "ok" | "error" | "cancelled" }[]
  /** Exchanges the remote made, in order, each with the types of its messages. */
  exchanges: string[][]
}

const failure = (kind: CodemodeError["kind"], message: string): { ok: false; error: CodemodeError } => ({ ok: false, error: { kind, message } })

/** `remote` is a stub of the sandbox object, or anything with `execute`. */
export function runRemote(remote: CodemodeRemote, code: string, options: RunOptions = {}): Promise<RunResult> {
  const tools = options.tools ?? {}
  const data: VmStartData = {
    code,
    tools: Object.keys(tools).map(name => ({ name, jsName: name.replace(/[^A-Za-z0-9_$]/g, "_"), description: "" })),
    globals: [],
    memoryLimitBytes: options.memoryLimitBytes,
    store: options.store ?? {},
  }
  const request: CodemodeRemoteRequest = options.interruptBudget === undefined ? { data } : { data, interruptBudget: options.interruptBudget }
  return new Promise<RunResult>(resolve => {
    const output: CodemodeOutputItem[] = []
    const calls: RunResult["calls"] = []
    const exchanges: string[][] = []
    const controller = new AbortController()
    const waiting = new Map<number, (reply: CodemodeRemoteReply) => void>()
    let finished = false
    let settled = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const finish = (result: RunResult["result"]) => {
      if (finished) return
      finished = true
      if (timer) clearTimeout(timer)
      controller.abort()
      for (const reply of waiting.values()) reply({ type: "stop" })
      waiting.clear()
      resolve({ result, output, calls, exchanges })
    }
    if (options.timeoutMs !== undefined) timer = setTimeout(() => finish(failure("timeout", `Execution timed out after ${options.timeoutMs} ms`)), options.timeoutMs)
    options.signal?.addEventListener("abort", () => finish(failure("aborted", "This operation was aborted")), { once: true })

    const reply = (id: number, ok: boolean, payload: string | undefined) => {
      if (finished) return
      const waiter = waiting.get(id)
      if (!waiter) return
      waiting.delete(id)
      waiter({ type: "result", id, ok, payload })
    }
    const runTool = (message: Extract<CodemodeRemoteMessage, { type: "call" }>) => {
      const record: RunResult["calls"][number] = { name: message.name, status: "cancelled" }
      calls.push(record)
      const tool = tools[message.name]
      Promise.resolve()
        .then(() => {
          if (!tool) throw new Error(`Unknown ${message.target} "${message.name}"`)
          return tool(message.args === undefined ? undefined : JSON.parse(message.args), { signal: controller.signal })
        })
        .then(
          value => { if (!finished) record.status = "ok"; reply(message.id, true, value === undefined ? undefined : JSON.stringify(value)) },
          (error: unknown) => { if (!finished) record.status = "error"; reply(message.id, false, error instanceof Error ? error.message : String(error)) },
        )
    }
    const handle = (message: CodemodeRemoteMessage) => {
      if (finished) return
      switch (message.type) {
        case "output": output.push(message.item); break
        case "call": runTool(message); break
        case "done":
          settled = true
          if (message.ok) finish({ ok: true, value: message.value === undefined ? undefined : JSON.parse(message.value) })
          else { const e = JSON.parse(message.error) as { name?: string; message: string; stack?: string }; finish({ ok: false, error: { kind: "script", ...e } }) }
          break
        case "crash": settled = true; finish(failure("sandbox", message.message)); break
        case "failure": settled = true; finish({ ok: false, error: message.error }); break
      }
    }

    const exchange: CodemodeRemoteExchange = messages => new Promise<CodemodeRemoteReply>(done => {
      if (finished || !Array.isArray(messages)) { done({ type: "stop" }); return }
      exchanges.push(messages.map(m => m.type))
      // Register the waiter first: the tool may answer before this function returns.
      const call = messages.find(m => m.type === "call")
      if (call?.type === "call") waiting.set(call.id, done)
      else done(null)
      for (const message of messages) handle(message)
    })
    Promise.resolve()
      .then(() => remote.execute(request, exchange))
      .then(
        () => { if (!settled && !finished) finish(failure("sandbox", "Remote sandbox returned before the script settled")) },
        (error: unknown) => { if (!finished) finish(failure("sandbox", `Remote sandbox failed: ${error instanceof Error ? error.message : String(error)}`)) },
      )
  })
}
