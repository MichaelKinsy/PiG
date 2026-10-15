// The comparison of allocs.mjs: a package may not gain allocation sites, and a fall asks for a lower baseline.
//
//   node --test durable/core/budget/allocs.test.mjs
import assert from "node:assert/strict"
import { test } from "node:test"
import { compare, sitesOf } from "./allocs.mjs"

test("sites are counted per package of the core (a report line has a line and column, or only the file), and the standard library's apart", () => {
  const core = new URL("..", import.meta.url).pathname.replace(/\/$/, "")
  const text = [
    `${core}/graph.go:112:6: object allocated on the heap: escapes at line 37`,
    `${core}/jv/value.go:5:2: object allocated on the heap: escapes at line 9`,
    `${core}/jv/parse.go:7:2: object allocated on the heap: escapes at line 9`,
    `/go/src/slices/sort.go:1:1: object allocated on the heap: escapes at line 3`,
    `${core}/session/runtime.go: object allocated on the heap: escapes at line 232`,
    `${core}/jv/value.go:5:2: not a report line`,
  ].join("\n")
  assert.deepEqual(sitesOf(text), { ".": 1, jv: 2, session: 1, stdlib: 1 })
})

test("a package with more sites, or one the baseline lacks, fails; fewer sites ask for a lower baseline", () => {
  const baseline = { ".": 10, jv: 5, rec: 2 }
  assert.deepEqual(compare(baseline, { ".": 10, jv: 5, rec: 2 }), { higher: [], lower: [] })
  assert.equal(compare(baseline, { ".": 11, jv: 5, rec: 2 }).higher.length, 1)
  assert.equal(compare(baseline, { ".": 10, jv: 5, rec: 2, turn: 1 }).higher.length, 1)
  assert.equal(compare(baseline, { ".": 10, jv: 4, rec: 2 }).lower.length, 1)
  assert.equal(compare(baseline, { ".": 10, jv: 5 }).lower.length, 1)
})
