// The body recipe of a model_context effect (ABI section 10.2): the request's JSON text as the segments it is made of.
//   recipe  := u32 nSegments, segment*
//   segment := u8 0, u32 len, bytes                 text the core built
//            | u8 1, u32 conv, u32 head, u32 tail    the model messages of a committed context (comma-separated JSON)
// A host expands it by deriving the contexts its ranges name from storage.

export class RecipeError extends Error {}

const NULL_TEXT = new TextEncoder().encode("null")

/**
 * The text a recipe stands for. A range segment is handed to `range`, which keeps the messages it derives, and stands for
 * `null` in the text: the caller puts the derived messages where the `null` parsed to, so they are never serialized and
 * parsed again.
 */
export function expandRecipe(recipe: Uint8Array, range?: (conv: number, head: number, tail: number) => void): Uint8Array {
  const dv = new DataView(recipe.buffer, recipe.byteOffset, recipe.byteLength)
  const bad = () => new RecipeError("malformed recipe")
  if (recipe.byteLength < 4) throw bad()
  const n = dv.getUint32(0, true)
  let at = 4
  const parts: Uint8Array[] = []
  let size = 0
  for (let i = 0; i < n; i++) {
    if (at >= recipe.byteLength) throw bad()
    const kind = recipe[at++]
    if (kind === 0) {
      if (at + 4 > recipe.byteLength) throw bad()
      const len = dv.getUint32(at, true)
      at += 4
      if (at + len > recipe.byteLength) throw bad()
      parts.push(recipe.subarray(at, at + len))
      at += len
      size += len
    } else if (kind === 1) {
      if (range === undefined || at + 12 > recipe.byteLength) throw bad()
      range(dv.getUint32(at, true), dv.getUint32(at + 4, true), dv.getUint32(at + 8, true))
      parts.push(NULL_TEXT)
      at += 12
      size += NULL_TEXT.length
    } else throw bad()
  }
  if (at !== recipe.byteLength) throw bad()
  const out = new Uint8Array(size)
  let to = 0
  for (const part of parts) { out.set(part, to); to += part.length }
  return out
}
