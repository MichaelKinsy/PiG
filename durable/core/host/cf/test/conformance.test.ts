// ABI 1 binding conformance (ABI section 12): the probe's event scripts run through the Wasm module and must produce the
// step bytes the native Go Step produced (probe/testdata/*.steps, written by `go test ./probe -update`).
import assert from "node:assert/strict"
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"
import { test } from "node:test"
import { ABI_ID, EVENT } from "../src/abi.ts"
import abiJson from "../../../abi/abi.json" with { type: "json" }
import { parseScript } from "../src/script.ts"
import { AbiMismatch, CoreTrap, WasmCore } from "../src/wasm.ts"
import { probeModule, root, toolchain } from "./helpers.ts"

const dir = join(root, "..", "..", "probe", "testdata")
const hex = (b: Uint8Array) => Buffer.from(b).toString("hex")

for (const file of readdirSync(dir).filter(f => f.endsWith(".events"))) {
  test(`probe ${file}: Wasm step bytes equal the native step bytes`, () => {
    // The native test fixes the uuid the same way (probe_test.go); the import is the only entropy the probe reads.
    const core = new WasmCore(probeModule(), { uuidv7: () => "01234567-89ab-7cde-8f01-23456789abcd" })
    assert.equal(core.abiId, ABI_ID)
    const session = core.newSession()
    session.keepRaw = true
    const golden = readFileSync(join(dir, file.replace(".events", ".steps")), "utf8").trim().split("\n")
    const events = parseScript(readFileSync(join(dir, file), "utf8"))
    assert.equal(golden.length, events.length)
    events.forEach((ev, i) => {
      const step = ev.rows ? session.rows(ev.rows, ev.now) : session.step(ev.kind, ev.payload, ev.now)
      assert.equal(hex(step.raw!), golden[i], `step ${i} (${ev.name}) differs`)
    })
  })
}

test("the sql_table export is the probe's table", () => {
  const core = new WasmCore(probeModule())
  assert.ok(core.sqlTable.length >= 8)
  assert.match(core.sqlTable.find(q => q.startsWith("UPDATE durable_metadata"))!, /AND next_seq = \?$/)
})

test("an abi_id mismatch is a startup error", () => {
  assert.throws(() => new WasmCore(probeModule(), { expectedAbiId: "0".repeat(64) }), AbiMismatch)
})

test("two sessions on one instance are independent handles", () => {
  const core = new WasmCore(probeModule())
  const a = core.newSession(), b = core.newSession()
  assert.notEqual(a.handle, b.handle)
  const open = parseScript(readFileSync(join(dir, "turn.events"), "utf8"))[0]!
  assert.equal(a.step(open.kind, open.payload, 1).reads.length, 1)
  assert.equal(b.step(open.kind, open.payload, 1).reads.length, 1)
  a.free()
  assert.throws(() => a.step(open.kind, open.payload, 1), CoreTrap)
  assert.equal(b.step(open.kind, open.payload, 1).reads.length, 1)
})

test("the module imports exactly the WASI set the ABI lists plus pig.uuidv7", () => {
  const listed = new Set(abiJson.imports.map(i => i.name))
  const imported = WebAssembly.Module.imports(probeModule()).map(i => `${i.module}.${i.name}`)
  assert.ok(imported.includes("pig.uuidv7"))
  for (const name of imported) assert.ok(listed.has(name), `${name} is not in the ABI import table`)
})

test("the host reserves the input buffer once and again only for a larger payload, growing memory safely", () => {
  const core = new WasmCore(probeModule())
  const session = core.newSession()
  const open = parseScript(readFileSync(join(dir, "turn.events"), "utf8"))[0]!
  for (let i = 0; i < 5; i++) session.step(open.kind, open.payload, 1)
  assert.equal(core.reservations, 1, "small payloads reuse the first reservation")
  // A payload far beyond the memory the module holds grows memory inside in_reserve: the view the host builds must come after it.
  const before = core.memoryBytes
  const big = new Uint8Array(before + 4 * 1024 * 1024)
  const step = session.step(EVENT.timer, big, 1)
  assert.ok(core.memoryBytes > before, "the reservation grew linear memory")
  assert.equal(core.reservations, 2, "a larger payload reserves again")
  assert.ok(step.status !== undefined)
})

// The TinyGo core is linked with a Wasm memory cap (build-wasm.sh CORE_MAX_MEMORY, 96 MiB: what leaves a 128 MiB Durable Object
// room for the host's share) and grows its heap by a fixed step up to it (budget/tinygo/src/runtime/arch_tinygowasm.go), so exhausting memory traps
// instead of growing until the isolate is killed. The standard Go build has no linker cap (the host recycles an instance above its high-water mark instead).
test("tinygo: asking for more memory than the cap traps, leaves the isolate alive and the cap holds", { skip: toolchain !== "tinygo" }, () => {
  const CAP = 96 * 1024 * 1024
  const core = new WasmCore(probeModule())
  assert.throws(() => core.reserveInput(CAP + 4 * 1024 * 1024), CoreTrap)
  assert.ok(core.memoryBytes <= CAP, `memory ${core.memoryBytes} stays within the cap`)
  // a request that fits still works on a fresh instance of the same module
  const fresh = new WasmCore(probeModule())
  assert.ok(fresh.reserveInput(8 * 1024 * 1024) > 0)
})
