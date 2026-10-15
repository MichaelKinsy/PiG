// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Pi 97195ec3 harness/tool.ts structuredOutputError: the host judges a result's structuredOutput against the registration's schema.
import assert from "node:assert/strict"
import { test } from "node:test"
import { structuredOutputError } from "../src/facade/structured.ts"

const schema = { type: "object", properties: { n: { type: "number" }, nested: { type: "object", properties: { s: { type: "string" } }, required: ["s"] } }, required: ["n"] }

test("structuredOutputError: no schema and no value is fine; a value without a schema is the tool's fault", () => {
  assert.equal(structuredOutputError({ name: "t" }, {}), undefined)
  assert.equal(structuredOutputError({ name: "t" }, { structuredOutput: { n: 1 } }), "Tool t returned structuredOutput but declares no structuredOutputSchema")
})

test("structuredOutputError: a schema requires a value unless the result is an error", () => {
  assert.equal(structuredOutputError({ name: "t", structuredOutputSchema: schema }, {}), "Tool t returned no structuredOutput")
  assert.equal(structuredOutputError({ name: "t", structuredOutputSchema: schema }, { isError: true }), undefined)
  assert.equal(structuredOutputError({ name: "t", structuredOutputSchema: schema }, { structuredOutput: { n: 2, nested: { s: "x" } } }), undefined)
})

test("structuredOutputError: the first schema error names its path (root when the path is empty) and message", () => {
  const tool = { name: "t", structuredOutputSchema: schema }
  assert.match(structuredOutputError(tool, { structuredOutput: { n: "x" } })!, /^Tool t returned structuredOutput that does not match its schema: n: .+/)
  assert.match(structuredOutputError(tool, { structuredOutput: { n: 1, nested: {} } })!, /^Tool t returned structuredOutput that does not match its schema: nested(\.s)?: .+/)
  assert.match(structuredOutputError(tool, { structuredOutput: 5 })!, /^Tool t returned structuredOutput that does not match its schema: root: .+/)
})
