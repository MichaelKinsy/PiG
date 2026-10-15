// The codemode sandbox Durable Object in workerd (Miniflare): an agent object runs scripts through CodemodeSandboxDurableObject over
// Workers RPC, with its tools in the agent (Pi's examples/cloudflare). Wrangler's CompiledWasm rule is Miniflare's CompiledWasm
// module type, so the object imports the same static ./quickjs.wasm and ./codemode.wasm a deployed Worker does.
import assert from "node:assert/strict"
import { join } from "node:path"
import { after, before, test } from "node:test"
import { call, startWorker, type WorkerSpec } from "./mf.ts"
import { codemodeWasmPath, quickjsWasmPath } from "./codemode-helpers.ts"
import { root, tempDir } from "./helpers.ts"
import type { RunResult } from "./worker/codemode-client.ts"

type Reply = RunResult & { toolLog: string[] }

const out = tempDir("codemode-do")
const spec: WorkerSpec = {
  entry: join(root, "test", "worker", "codemode-do.ts"),
  outDir: join(out, "dist"),
  objects: { SANDBOX: "SandboxDO", LOW: "LowSandboxDO", AGENT: "AgentDO" },
  wasm: { "./quickjs.wasm": quickjsWasmPath, "./codemode.wasm": codemodeWasmPath() },
}
let mf: Awaited<ReturnType<typeof startWorker>>
before(async () => { mf = await startWorker(spec, join(out, "persist")) })
after(async () => { await mf?.dispose() })
const run = (body: object) => call<Reply>(mf, "/", body)

test("a script runs in the sandbox object and its tools run in the agent object, over Workers RPC", async () => {
  const r = await run({ sandbox: "SANDBOX", code: "text('hi'); const a = await tools.echo({ n: 1 }); const w = await tools.where(); return { a, w }" })
  assert.deepEqual(r.result, { ok: true, value: { a: { n: 1 }, w: { isolate: "agent", toolCalls: 2 } } })
  assert.deepEqual(r.output, [{ type: "text", text: "hi" }])
  assert.deepEqual(r.toolLog, ["echo", "where"], "the tools ran in the agent")
  assert.deepEqual(r.calls, [{ name: "echo", status: "ok" }, { name: "where", status: "ok" }])
})

test("output is batched across the RPC boundary: 5000 console lines make at most six exchanges", async () => {
  const r = await run({ sandbox: "SANDBOX", code: "for (let i = 0; i < 5000; i++) console.log(i); return 'ok'" })
  assert.deepEqual(r.result, { ok: true, value: "ok" })
  assert.equal(r.output.length, 5000)
  assert.ok(r.exchanges.length <= 6, `${r.exchanges.length} exchanges`)
})

test("the object's limits cap every request: the interrupt budget and the VM memory", async () => {
  const budget = await run({ sandbox: "LOW", code: "while (true) {}", interruptBudget: 1e9 })
  assert.deepEqual(budget.result, { ok: false, error: { kind: "timeout", message: "Execution exceeded its interrupt budget of 1000" } })
  const memory = await run({ sandbox: "LOW", code: "const parts = []; for (;;) parts.push('x'.repeat(1 << 20) + parts.length)", memoryLimitBytes: 1 << 30 })
  assert.equal(memory.result.ok, false)
  assert.match(JSON.stringify(memory.result), /out of memory/)
})

test("the default object allows what the low object refuses, so the limits come from the class", async () => {
  const r = await run({ sandbox: "SANDBOX", code: "let n = 0; for (let i = 0; i < 3e5; i++) n += i; return n" })
  assert.equal(r.result.ok, true)
  const low = await run({ sandbox: "LOW", code: "let n = 0; for (let i = 0; i < 3e7; i++) n += i; return n" })
  assert.equal(low.result.ok, false)
  const hold = "const parts = []; for (let i = 0; i < 6; i++) parts.push('x'.repeat(1 << 20) + i); return parts.length"
  assert.deepEqual((await run({ sandbox: "SANDBOX", code: hold })).result, { ok: true, value: 6 })
  const capped = await run({ sandbox: "LOW", code: hold, memoryLimitBytes: 1 << 30 })
  assert.match(JSON.stringify(capped.result), /out of memory/, "6 MiB is over the low object's 4 MiB, whatever the request asks")
})

test("the caller aborting stops the remote: the tool is cancelled and the object serves the next execution", async () => {
  const started = Date.now()
  const r = await run({ sandbox: "SANDBOX", code: "await tools.sleep({ ms: 60000 }); return 'never'", abortAfterMs: 200 })
  assert.equal((r.result as { error: { kind: string } }).error.kind, "aborted")
  assert.deepEqual(r.calls, [{ name: "sleep", status: "cancelled" }])
  assert.ok(Date.now() - started < 10_000)
  const next = await run({ sandbox: "SANDBOX", code: "return 1 + 1" })
  assert.deepEqual(next.result, { ok: true, value: 2 })
})

test("the caller's deadline ends a script that waits on a tool, and executions in one object do not share state", async () => {
  const slow = await run({ sandbox: "SANDBOX", code: "globalThis.leak = 1; await tools.sleep({ ms: 60000 })", timeoutMs: 300 })
  assert.deepEqual(slow.result, { ok: false, error: { kind: "timeout", message: "Execution timed out after 300 ms" } })
  const after = await run({ sandbox: "SANDBOX", code: "return typeof globalThis.leak" })
  assert.deepEqual(after.result, { ok: true, value: "undefined" })
})

// The TypeScript client of the Cloudflare entry (src/codemode/cloudflare.ts) in workerd: createInlineCodemodeSandbox runs the script on the
// agent's own thread with the modules Wrangler bundled, and RemoteCodemodeSandbox (Pi's client) reaches the sandbox object over Workers RPC.
for (const client of ["inline", "remote"] as const) {
  test(`${client}: the entry's client runs a script with tools in the agent and reports calls, output and the budget`, async () => {
    const r = await run({ sandbox: "SANDBOX", client, code: "console.log('hi'); const a = await tools.echo({ n: 1 }); return { a, w: await tools.where() }" })
    assert.deepEqual(r.result, { ok: true, value: { a: { n: 1 }, w: { isolate: "agent", toolCalls: 2 } } })
    assert.deepEqual(r.output, [{ type: "text", text: "hi", console: true }])
    assert.deepEqual(r.toolLog, ["echo", "where"])
    assert.deepEqual(r.calls, [{ name: "echo", status: "ok" }, { name: "where", status: "ok" }])
    const budget = await run({ sandbox: "LOW", client, code: "while (true) {}", interruptBudget: 1000 })
    assert.deepEqual(budget.result, { ok: false, error: { kind: "timeout", message: "Execution exceeded its interrupt budget of 1000" } })
  })
}
