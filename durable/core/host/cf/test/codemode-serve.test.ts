// The codemode module on the Worker host (src/codemode/serve.ts) without a Worker: the real TinyGo module and QuickJS serve one
// execution at a time to the client of test/worker/codemode-client.ts, through the remote protocol. The cases are the
// serve-side cases of Pi's packages/codemode/test/portable.test.ts ("RemoteCodemodeSandbox"), with the same inputs and expectations,
// plus the cases only a step-machine host can get wrong (concurrent exchanges, a rejected exchange, a stop before the first call).
import assert from "node:assert/strict"
import { test } from "node:test"
import { serveCodemodeRemote, type CodemodeServeOptions } from "../src/codemode/serve.ts"
import type { CodemodeRemote, CodemodeRemoteExchange, CodemodeRemoteMessage, CodemodeRemoteReply, CodemodeRemoteRequest } from "../src/codemode/protocol.ts"
import { codemodeModules } from "./codemode-helpers.ts"
import { runRemote, type RunOptions } from "./worker/codemode-client.ts"

/** An in-process remote that records what crosses the boundary, cloned as Workers RPC would (portable.test.ts recordingRemote). */
function recordingRemote(options: Partial<CodemodeServeOptions> = {}) {
  const exchanges: number[] = []
  const served: Promise<void>[] = []
  const remote: CodemodeRemote = {
    execute(request: CodemodeRemoteRequest, exchange: CodemodeRemoteExchange) {
      const done = serveCodemodeRemote(
        structuredClone(request),
        async messages => {
          exchanges.push(messages.length)
          return structuredClone(await exchange(structuredClone(messages)))
        },
        { modules: codemodeModules(), ...options },
      )
      served.push(done)
      return done
    },
  }
  return { remote, exchanges, served }
}

const run = (code: string, options?: RunOptions, limits?: Partial<CodemodeServeOptions>) => runRemote(recordingRemote(limits).remote, code, options)

test("a script returns a value and its output arrives in order, with the store snapshot loaded", async () => {
  const r = await run(`console.log("a"); text("b"); return { n: 1 + 2, k: await load("k") }`, { store: { k: "7" } })
  assert.deepEqual(r.result, { ok: true, value: { n: 3, k: 7 } })
  assert.deepEqual(r.output, [{ type: "text", text: "a", console: true }, { type: "text", text: "b" }])
})

test("runs tools in the caller and the script on the remote (portable.test.ts: runs tools in the caller and the script on the remote)", async () => {
  const { remote, served } = recordingRemote()
  const seen: unknown[] = []
  const r = await runRemote(remote, "text('hi'); return await tools.add({ a: 1 })", {
    tools: { add: args => { seen.push(args); return (args as { a: number }).a + 1 } },
    store: { k: "1" },
  })
  assert.deepEqual(r.result, { ok: true, value: 2 })
  assert.deepEqual(r.output, [{ type: "text", text: "hi" }])
  assert.deepEqual(seen, [{ a: 1 }])
  assert.deepEqual(r.calls, [{ name: "add", status: "ok" }])
  await Promise.all(served)
})

test("batches output instead of making one exchange per line (portable.test.ts: batches output ...)", async () => {
  const { remote, exchanges } = recordingRemote()
  const r = await runRemote(remote, "for (let i = 0; i < 5000; i++) console.log(i); return 'ok'")
  assert.deepEqual(r.result, { ok: true, value: "ok" })
  assert.equal(r.output.length, 5000)
  assert.deepEqual(r.output[4999], { type: "text", text: "4999", console: true })
  assert.ok(exchanges.length <= 6, `${exchanges.length} exchanges`)
})

test("caps the interrupt budget and memory at the remote's limits (portable.test.ts: caps the interrupt budget and memory ...)", async () => {
  const { remote } = recordingRemote({ maxInterruptBudget: 1_000, maxMemoryLimitBytes: 8 * 1024 * 1024 })
  const budget = await runRemote(remote, "while (true) {}", { interruptBudget: 1e9 })
  assert.deepEqual(budget.result, { ok: false, error: { kind: "timeout", message: "Execution exceeded its interrupt budget of 1000" } })
  const memory = await runRemote(remote, "const parts = []; for (;;) parts.push('x'.repeat(1 << 20) + parts.length);")
  assert.equal(memory.result.ok, false)
  assert.match(JSON.stringify(memory), /out of memory/)
})

test("the remote's memory limit is the QuickJS heap limit: a script under it runs, a script over it runs out of memory, and a smaller request lowers it", async () => {
  const hold = (mib: number) => `const parts = []; for (let i = 0; i < ${mib}; i++) parts.push('x'.repeat(1 << 20) + i); return parts.length`
  const { remote } = recordingRemote({ maxMemoryLimitBytes: 8 * 1024 * 1024 })
  assert.deepEqual((await runRemote(remote, hold(4))).result, { ok: true, value: 4 })
  const over = await runRemote(remote, hold(12))
  assert.equal(over.result.ok, false)
  assert.match(JSON.stringify(over), /out of memory/)
  // A request may ask for less than the remote allows, never more.
  const lower = await runRemote(remote, hold(4), { memoryLimitBytes: 2 * 1024 * 1024 })
  assert.match(JSON.stringify(lower), /out of memory/)
  const higher = await runRemote(remote, hold(12), { memoryLimitBytes: 64 * 1024 * 1024 })
  assert.match(JSON.stringify(higher), /out of memory/, "the request cannot raise the remote's limit")
  // Without any limit the same script runs.
  assert.deepEqual((await runRemote(recordingRemote().remote, hold(12))).result, { ok: true, value: 12 })
})

test("tells the remote to stop when the caller aborts, and the served execution ends (portable.test.ts: tells the remote to stop ...)", async () => {
  const { remote, served } = recordingRemote()
  let started!: () => void
  const call = new Promise<void>(resolve => { started = resolve })
  const controller = new AbortController()
  const pending = runRemote(remote, "await tools.hang(); return 'never'", {
    tools: { hang: () => { started(); return new Promise(() => {}) } },
    signal: controller.signal,
  })
  await call
  controller.abort()
  const r = await pending
  assert.equal(r.result.ok, false)
  assert.equal((r.result as { error: { kind: string } }).error.kind, "aborted")
  // The remote's pending exchange is answered with "stop", so its VM is disposed and execute() returns.
  await Promise.all(served)
})

test("concurrent tool calls are concurrent exchanges whose replies may arrive in any order", async () => {
  const order: string[] = []
  const wait = (ms: number, tag: string) => new Promise(resolve => setTimeout(() => { order.push(tag); resolve(tag) }, ms))
  const r = await run("return await Promise.all([tools.slow({ ms: 60, tag: 'a' }), tools.slow({ ms: 5, tag: 'b' }), tools.slow({ ms: 30, tag: 'c' })])", {
    tools: { slow: args => wait((args as { ms: number }).ms, (args as { tag: string }).tag) },
  })
  assert.deepEqual(r.result, { ok: true, value: ["a", "b", "c"] })
  assert.deepEqual(order, ["b", "c", "a"], "the tools finished out of call order")
  assert.deepEqual(r.calls.map(c => c.status), ["ok", "ok", "ok"])
})

test("a tool that throws, an unknown tool and a script that throws are reported to the script and the caller as Pi does", async () => {
  const thrown = await run("try { await tools.fail() } catch (e) { return 'caught: ' + e.message }", { tools: { fail: () => { throw new Error("tool failed") } } })
  assert.deepEqual(thrown.result, { ok: true, value: "caught: tool failed" })
  assert.deepEqual(thrown.calls, [{ name: "fail", status: "error" }])
  const script = await run("throw new TypeError('nope')")
  assert.equal(script.result.ok, false)
  assert.deepEqual({ ...(script.result as { error: object }).error, stack: undefined }, { kind: "script", name: "TypeError", message: "nope", stack: undefined })
  const syntax = await run("return (")
  assert.equal((syntax.result as { error: { kind: string; name: string } }).error.kind, "script")
  assert.equal((syntax.result as { error: { name: string } }).error.name, "SyntaxError")
})

test("runaway recursion is a catchable RangeError at the in-thread stack guard, not a trap", async () => {
  const r = await run("function f() { return f() + 1 } try { f() } catch (e) { return e.constructor.name }")
  assert.deepEqual(r.result, { ok: true, value: "RangeError" })
})

test("the interrupt budget counts across waits on tools (portable.test.ts: counts the budget across waits on tools)", async () => {
  const { remote } = recordingRemote({ maxInterruptBudget: 1_000 })
  const r = await runRemote(remote, "for (;;) { for (let i = 0; i < 1e5; i++); await tools.tick(); }", { tools: { tick: () => 1 } })
  assert.deepEqual(r.result, { ok: false, error: { kind: "timeout", message: "Execution exceeded its interrupt budget of 1000" } })
  assert.ok(r.calls.length > 1)
  for (const code of ["while (true) {}", "while (true) await null"]) {
    const budget = await runRemote(remote, code, { interruptBudget: 1_000 })
    assert.deepEqual(budget.result, { ok: false, error: { kind: "timeout", message: "Execution exceeded its interrupt budget of 1000" } })
  }
})

test("many sequential calls and several executions at once each get their own instances", async () => {
  const { remote } = recordingRemote()
  let n = 0
  const sequential = runRemote(remote, "let s = 0; for (let i = 0; i < 500; i++) s += await tools.inc(); return s", { tools: { inc: () => ++n } })
  const others = [1, 2, 3].map(k => runRemote(remote, `text('k${k}'); return await tools.id(${k})`, { tools: { id: args => args } }))
  const results = await Promise.all([sequential, ...others])
  assert.deepEqual(results[0]!.result, { ok: true, value: (500 * 501) / 2 })
  assert.deepEqual(results.slice(1).map(r => r.result), [1, 2, 3].map(value => ({ ok: true, value })))
  assert.deepEqual(results.slice(1).map(r => r.output), [1, 2, 3].map(k => [{ type: "text", text: `k${k}` }]))
})

test("an exchange that rejects ends the served execution without hanging, and the VM is dropped", async () => {
  let calls = 0
  const done = serveCodemodeRemote(
    { data: { code: "await tools.t(); await tools.t(); return 1", tools: [{ name: "t", jsName: "t", description: "" }], globals: [], memoryLimitBytes: undefined, store: {} } },
    () => { calls++; return Promise.reject(new Error("rpc failed")) },
    { modules: codemodeModules() },
  )
  await done
  assert.equal(calls, 1, "the execution ended at the failed exchange; no second call was made")
})

test("an exchange that throws synchronously ends the served execution too", async () => {
  await serveCodemodeRemote(
    { data: { code: "await tools.t(); return 1", tools: [{ name: "t", jsName: "t", description: "" }], globals: [], memoryLimitBytes: undefined, store: {} } },
    () => { throw new Error("stub gone") },
    { modules: codemodeModules() },
  )
})

test("a stop reply to the first exchange ends the execution with no terminal message and later replies are ignored", async () => {
  const seen: string[][] = []
  await serveCodemodeRemote(
    { data: { code: "await tools.t(); await tools.t(); return 1", tools: [{ name: "t", jsName: "t", description: "" }], globals: [], memoryLimitBytes: undefined, store: {} } },
    async messages => { seen.push(messages.map(m => m.type)); return { type: "stop" } },
    { modules: codemodeModules() },
  )
  assert.deepEqual(seen, [["call"]])
})

/** A client whose answer to each exchange arrives `ms` later; `log` records when each settles and when the served execution resolves. */
const slowClient = (log: string[], ms = 30) => async (messages: unknown[]) => {
  log.push(`sent ${(messages as { type: string }[]).map(m => m.type).join(",")}`)
  await new Promise(resolve => setTimeout(resolve, ms))
  log.push("answered")
  return null
}

test("a module that cannot be instantiated is a sandbox failure, and the execution resolves only after the client took it (Pi's serveCodemodeRemote: the launcher rejected)", async () => {
  const log: string[] = []
  const messages: unknown[] = []
  const exchange = slowClient(log)
  await serveCodemodeRemote(
    { data: { code: "return 1", tools: [], globals: [], memoryLimitBytes: undefined, store: {} } },
    async m => { messages.push(...m); return exchange(m) },
    { modules: { quickjs: codemodeModules().codemode, codemode: codemodeModules().quickjs } },
  )
  log.push("resolved")
  assert.deepEqual(log, ["sent failure", "answered", "resolved"])
  assert.equal(messages.length, 1)
  const failure = messages[0] as { type: string; error: { kind: string; message: string } }
  assert.equal(failure.type, "failure")
  assert.equal(failure.error.kind, "sandbox")
  assert.ok(failure.error.message.length > 0)
})

test("a trap inside the module is a crash named as Pi's VM names it, delivered before the execution resolves; a rejected delivery still resolves", async () => {
  // A memory limit the module cannot decode makes it panic, which TinyGo turns into a trap.
  const request = { data: { code: "return 1", tools: [], globals: [], memoryLimitBytes: undefined, store: {} } }
  const log: string[] = []
  const messages: unknown[] = []
  const exchange = slowClient(log)
  await serveCodemodeRemote(request, async m => { messages.push(...m); return exchange(m) }, { modules: codemodeModules(), maxMemoryLimitBytes: -1 })
  log.push("resolved")
  assert.deepEqual(log, ["sent crash", "answered", "resolved"])
  assert.deepEqual(messages, [{ type: "crash", message: "RuntimeError: unreachable" }])
  await serveCodemodeRemote(request, () => Promise.reject(new Error("gone")), { modules: codemodeModules(), maxMemoryLimitBytes: -1 })
})

// Output is kept host-side (serve.ts HeldOutput): the module hands each item to the host as it prints it, and a batch takes the items it
// carries by count. A trap of the module then loses nothing: the crash message follows the output the script printed, as Pi's
// serveCodemodeRemote delivers its buffer with the crash. The trap is made after the module's second step by wrapping the instance's
// codemode_out_count export, which every step calls once after the module has run, so the items of that step are held and no exchange
// has carried them yet.
test("a trap of the module delivers the output the script printed before it, in front of the crash", async () => {
  const modules = codemodeModules()
  const Instance = WebAssembly.Instance
  ;(WebAssembly as { Instance: unknown }).Instance = class extends Instance {
    constructor(module: WebAssembly.Module, imports?: WebAssembly.Imports) {
      super(module, imports)
      if (module !== modules.codemode) return
      const real = this.exports as Record<string, any>
      let steps = 0
      const exports = { ...real, codemode_out_count: () => { if (++steps === 2) throw new WebAssembly.RuntimeError("unreachable"); return real.codemode_out_count!() } }
      Object.defineProperty(this, "exports", { value: exports })
    }
  }
  const messages: CodemodeRemoteMessage[][] = []
  try {
    const request = { data: { code: `console.log("a"); await tools.t(); console.log("b"); text("c"); await tools.t(); return 1`, tools: [{ name: "t", jsName: "t", description: "" }], globals: [], memoryLimitBytes: undefined, store: {} } }
    await serveCodemodeRemote(request, async (m): Promise<CodemodeRemoteReply> => { messages.push(structuredClone(m)); const call = m.at(-1); return call?.type === "call" ? { type: "result", id: call.id, ok: true, payload: "null" } : null }, { modules })
  } finally { WebAssembly.Instance = Instance }
  assert.deepEqual(messages.map(batch => batch.map(m => m.type === "output" ? m.item.type === "text" ? m.item.text : "image" : m.type)), [["a", "call"], ["b", "c", "crash"]])
  assert.deepEqual(messages[1]!.at(-1), { type: "crash", message: "RuntimeError: unreachable" })
})

// The output limits (MaxOutputItems, MaxOutputChars of the portable sandbox) are met inside the module's heap: the sandbox.test.ts
// case "fails a script whose output passes the limits even if it catches the error", which the Go behavior backend of
// test/codemode-go-behavior.test.ts found trapping the module ('unreachable', its heap over the cap) before the exchanges were written
// by hand. No test guards the cap itself: both this case and that one pass with the module capped at 64 MiB.
test("a script at the output limits fails with a RangeError and keeps the output it printed, of 16 MiB or of 100,000 items", async () => {
  const text = await run(`const s = "x".repeat(1 << 20); for (;;) { try { text(s) } catch {} }`)
  assert.equal(text.result.ok, false)
  assert.equal((text.result as any).error.name, "RangeError")
  assert.match((text.result as any).error.message, /script output exceeded/)
  const chars = text.output.reduce((n, item) => n + ("text" in item ? item.text.length : item.data.length), 0)
  assert.ok(chars <= 16 * 1024 * 1024 && chars > 14 * 1024 * 1024, `${chars} characters`)

  const items = await run(`for (;;) text("")`)
  assert.equal((items.result as any).error.name, "RangeError")
  assert.equal(items.output.length, 100_000)
})

// With the module holding the output of the batch it is building (before the host kept it), 100,000 items of one character trapped the
// module ("unreachable", its heap over the cap) where 100,000 empty items did not; the host holds them now and the module's heap does not.
test("100,000 text items of one character are all delivered, held by the host and not by the module's heap", async () => {
  const r = await run(`for (let i = 0; i < 100000; i++) text("x"); return 1`)
  assert.deepEqual(r.result, { ok: true, value: 1 })
  assert.equal(r.output.length, 100_000)
})

// The heap budget (rev F17): a Durable Object's isolate is 128 MB for the V8 heap and every Wasm memory together. At the output limits
// the module's Go heap peaks at 64 MiB (its cap is 96) and QuickJS stays under its 32 MiB default limit plus its own overhead; this keeps
// the pair inside that budget so a change that costs the module more per message fails here. Miniflare does not enforce the isolate
// limit, so the sum is checked against the budget rather than relying on workerd to kill the object. The test sees each execution's two
// memories by wrapping WebAssembly.Instance; linear memory never shrinks, so its size after the execution is its peak.
test("a script at the output limits keeps the two Wasm memories inside the isolate's budget", async () => {
  const modules = codemodeModules()
  const pairs: { codemode?: WebAssembly.Memory; quickjs?: WebAssembly.Memory }[] = []
  const Instance = WebAssembly.Instance
  ;(WebAssembly as { Instance: unknown }).Instance = class extends Instance {
    constructor(module: WebAssembly.Module, imports?: WebAssembly.Imports) {
      super(module, imports)
      const memory = this.exports.memory as WebAssembly.Memory
      if (module === modules.quickjs) pairs.push({ quickjs: memory })
      else if (module === modules.codemode) pairs[pairs.length - 1]!.codemode = memory
    }
  }
  try {
    for (const code of [`const s = "x".repeat(1 << 20); for (;;) { try { text(s) } catch {} }`, `for (;;) text("")`]) {
      const r = await run(code)
      assert.equal((r.result as any).error?.name, "RangeError", JSON.stringify(r.result))
    }
  } finally {
    ;(WebAssembly as { Instance: unknown }).Instance = Instance
  }
  assert.equal(pairs.length, 2)
  const MiB = 1024 * 1024
  for (const { codemode, quickjs } of pairs) {
    const go = codemode!.buffer.byteLength, qjs = quickjs!.buffer.byteLength
    assert.ok(go <= 64 * MiB, `the codemode module peaked at ${(go / MiB).toFixed(1)} MiB`)
    assert.ok(qjs <= 64 * MiB, `QuickJS peaked at ${(qjs / MiB).toFixed(1)} MiB`)
    assert.ok(go + qjs <= 112 * MiB, `the two memories peaked at ${((go + qjs) / MiB).toFixed(1)} MiB`)
  }
})
