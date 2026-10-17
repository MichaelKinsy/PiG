// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Ports packages/durable/src/harness/tool.ts structuredOutputError (pi-durable main): the host judges a tool result's
// `structuredOutput` against the schema its registration declares, because the schema lives in the host. The core asks through
// the `checkStructuredOutput` hook after afterTool and before the result settles.
import { Compile } from "typebox/compile"

type Tool = { name: string; structuredOutputSchema?: object }
type Result = { structuredOutput?: unknown; isError?: boolean }

const structuredValidators = new WeakMap<object, ReturnType<typeof Compile>>()

/** Why a result's `structuredOutput` breaks the tool's contract, or `undefined` when it keeps it. */
export function structuredOutputError(tool: Tool, result: Result): string | undefined {
  const schema = tool.structuredOutputSchema
  const value = result.structuredOutput
  if (schema === undefined) return value === undefined ? undefined : `Tool ${tool.name} returned structuredOutput but declares no structuredOutputSchema`
  if (value === undefined) return result.isError === true ? undefined : `Tool ${tool.name} returned no structuredOutput`
  let validator = structuredValidators.get(schema)
  if (validator === undefined) {
    validator = Compile(schema as never)
    structuredValidators.set(schema, validator)
  }
  if (validator.Check(value)) return undefined
  const [first] = validator.Errors(value)
  const path = first?.instancePath.replace(/^\//, "").replace(/\//g, ".") || "root"
  return `Tool ${tool.name} returned structuredOutput that does not match its schema: ${path}: ${first?.message ?? "invalid"}`
}
