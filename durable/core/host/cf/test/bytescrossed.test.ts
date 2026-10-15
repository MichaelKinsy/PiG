// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// The count gate's bytesCrossed for a Wasm session (durable/contract/lib/counts.mjs): an event's payload and the step it answers
// count their bytes; a thin `hop` answer counts its 12 bytes and an empty one none. The core here is a stand-in whose exports
// return fixed step bytes, so every expected sum is the length of bytes the test wrote itself.
import assert from "node:assert/strict"
import { test } from "node:test"
import { STATUS } from "../src/abi.ts"
import { WasmSession, type WasmCore } from "../src/wasm.ts"
import { encodeRows, type Rows } from "../src/wire.ts"

const THIN = 128, LIST = 256, STEP = 512, INPUT = 1024
/** An ok step with no commits, reads, notices, effects or order: the u32 size, then 11 bytes. */
const EMPTY_STEP = 15

function standIn() {
  const buffer = new ArrayBuffer(4096)
  const dv = new DataView(buffer)
  dv.setUint32(STEP, EMPTY_STEP - 4, true)
  dv.setUint8(STEP + 4, STATUS.ok)
  dv.setUint32(LIST, 1, true); dv.setUint32(LIST + 4, STEP, true); dv.setUint32(LIST + 8, EMPTY_STEP, true)
  dv.setUint32(THIN, 0, true); dv.setUint32(THIN + 4, 9, true); dv.setUint32(THIN + 8, 3, true)
  let hopAnswer = 0
  const core = {
    bytesCrossed: 0,
    activeReader: undefined,
    activeTap: undefined,
    reserveInput: () => INPUT,
    x: { memory: { buffer }, step: () => LIST, hop: () => hopAnswer },
  }
  return { core, session: new WasmSession(core as unknown as WasmCore, 1), answerHop: (ptr: number) => { hopAnswer = ptr } }
}

test("a Wasm session counts the payload of each event and of the step it answers", () => {
  const { core, session } = standIn()
  session.step(1, new Uint8Array(10), 0)
  assert.equal(core.bytesCrossed, 10 + EMPTY_STEP, "an event with a payload")
  session.step(1, undefined, 0)
  assert.equal(core.bytesCrossed, 10 + 2 * EMPTY_STEP, "an event without one")
  const answers: Rows[] = [{ id: 4, cols: 2, rows: [["abc", 1], [null, new Uint8Array([1, 2, 3])]] }]
  const rows = encodeRows(answers, n => new Uint8Array(n))
  session.rows(answers, 0)
  assert.equal(core.bytesCrossed, 10 + 3 * EMPTY_STEP + rows, "a rows event: its encoding")
})

test("a hop counts what its answer carries: 12 bytes for a thin answer, none for an empty one, the step otherwise", () => {
  const { core, session, answerHop } = standIn()
  answerHop(THIN)
  assert.equal(session.hop(7, 0), 1)
  assert.deepEqual([session.hopId, session.hopTicks, core.bytesCrossed], [9, 3, 12])
  answerHop(0)
  assert.equal(session.hop(7, 0), 0)
  assert.equal(core.bytesCrossed, 12)
  answerHop(LIST)
  assert.equal(typeof session.hop(7, 0), "object")
  assert.equal(core.bytesCrossed, 12 + EMPTY_STEP)
})
