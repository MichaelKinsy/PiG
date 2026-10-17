import assert from "node:assert/strict"
import { test } from "node:test"
import { expandRecipe, RecipeError } from "../src/recipe.ts"

const enc = new TextEncoder()
const dec = new TextDecoder()

/** A recipe built the way the core's abi.Recipe does: text segments and range segments. */
function recipe(...segments: (string | [number, number, number])[]): Uint8Array {
  const parts: number[] = []
  const u32 = (v: number) => parts.push(v & 255, (v >>> 8) & 255, (v >>> 16) & 255, (v >>> 24) & 255)
  u32(segments.length)
  for (const s of segments) {
    if (typeof s === "string") { const b = enc.encode(s); parts.push(0); u32(b.length); parts.push(...b) } else { parts.push(1); for (const v of s) u32(v) }
  }
  return Uint8Array.from(parts)
}

test("a recipe expands to its text, each range standing for null and handed to the caller in order", () => {
  const seen: number[][] = []
  const out = expandRecipe(recipe(`{"context":{"messages":[`, [7, 0, 40], ",", [8, 12, 90], "]}}"), (c, h, t) => { seen.push([c, h, t]) })
  assert.equal(dec.decode(out), `{"context":{"messages":[null,null]}}`)
  assert.deepEqual(seen, [[7, 0, 40], [8, 12, 90]])
  assert.equal(dec.decode(expandRecipe(recipe("{", "}"))), "{}")
})

test("a malformed recipe, or a range without a caller to derive it, is an error", () => {
  const good = recipe("[", [1, 0, 5])
  for (const bad of [new Uint8Array(0), good.subarray(0, good.length - 1), Uint8Array.from([...good, 0]), Uint8Array.from([1, 0, 0, 0, 7]), Uint8Array.from([1, 0, 0, 0, 0, 9, 0, 0, 0, 97])]) {
    assert.throws(() => expandRecipe(bad, () => {}), RecipeError)
  }
  assert.throws(() => expandRecipe(good), RecipeError)
})
