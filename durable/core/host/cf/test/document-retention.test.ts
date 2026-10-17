// session.ts documentRetention / setDefaultDocumentRetention (Pi 97195ec3): the retention a Session resolves from a setting.
import assert from "node:assert/strict"
import { test } from "node:test"
import { documentRetention, setDefaultDocumentRetention } from "../src/facade/session/session.ts"

test("a retention that is a number of milliseconds, 0 or more, is kept; anything else is the default", () => {
  assert.equal(documentRetention(undefined), 600_000)
  assert.equal(documentRetention(0), 0)
  assert.equal(documentRetention(250), 250)
  assert.equal(documentRetention(Infinity), Infinity)
  for (const bad of [-1, Number.NaN, "5" as unknown as number, null as unknown as number]) {
    assert.equal(documentRetention(bad), 600_000, String(bad))
  }
})

test("the default is settable, and restored", () => {
  setDefaultDocumentRetention(0)
  try { assert.equal(documentRetention(undefined), 0); assert.equal(documentRetention(5), 5) } finally { setDefaultDocumentRetention(600_000) }
  assert.equal(documentRetention(undefined), 600_000)
})
