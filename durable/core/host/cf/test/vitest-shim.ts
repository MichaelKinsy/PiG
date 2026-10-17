// The subset of vitest's API that Pi's packages/codemode tests use (describe, describe.each, it, beforeEach, afterEach, expect with
// toBe/toEqual/toMatchObject/toContain/toMatch/toThrow/toHaveLength/toHaveProperty/comparisons, .not, .rejects, expect.stringContaining and
// expect.stringMatching), over node:test, so the ported Pi test files run here with their own assertions. Matcher semantics follow
// vitest's: toEqual ignores properties that are undefined, toMatchObject matches a subset of an object and arrays element for element.
import assert from "node:assert/strict"
import { afterEach, beforeEach, describe as nodeDescribe, it } from "node:test"

const ASYMMETRIC = Symbol("asymmetric")
type Asymmetric = { [ASYMMETRIC]: true; matches(actual: unknown): boolean; toString(): string }
const asymmetric = (description: string, matches: (actual: unknown) => boolean): Asymmetric => ({ [ASYMMETRIC]: true, matches, toString: () => description })
const isAsymmetric = (value: unknown): value is Asymmetric => typeof value === "object" && value !== null && ASYMMETRIC in value

function equals(actual: unknown, expected: unknown, subset: boolean): boolean {
  if (isAsymmetric(expected)) return expected.matches(actual)
  if (Object.is(actual, expected)) return true
  if (typeof actual !== "object" || typeof expected !== "object" || actual === null || expected === null) return false
  if (Array.isArray(actual) !== Array.isArray(expected)) return false
  if (Array.isArray(actual) && Array.isArray(expected)) return actual.length === expected.length && expected.every((item, i) => equals(actual[i], item, subset))
  const a = actual as Record<string, unknown>, e = expected as Record<string, unknown>
  for (const key of Object.keys(e)) {
    if (e[key] === undefined && !subset) continue
    if (!equals(a[key], e[key], subset)) return false
  }
  if (!subset) for (const key of Object.keys(a)) if (a[key] !== undefined && !(key in e)) return false
  return true
}

const show = (value: unknown) => { try { return JSON.stringify(value) ?? String(value) } catch { return String(value) } }

function matchers(actual: unknown, negate: boolean, message: string | undefined) {
  const check = (pass: boolean, what: string) => {
    if (pass === negate) assert.fail(`${message ? `${message}: ` : ""}expected ${show(actual)}${negate ? " not" : ""} ${what}`)
  }
  return {
    toBe: (expected: unknown) => check(Object.is(actual, expected), `to be ${show(expected)}`),
    toEqual: (expected: unknown) => check(equals(actual, expected, false), `to equal ${show(expected)}`),
    toMatchObject: (expected: unknown) => check(equals(actual, expected, true), `to match ${show(expected)}`),
    toContain: (expected: unknown) => check(typeof actual === "string" ? actual.includes(String(expected)) : Array.isArray(actual) && actual.includes(expected), `to contain ${show(expected)}`),
    toMatch: (expected: RegExp | string) => check(typeof actual === "string" && (typeof expected === "string" ? actual.includes(expected) : expected.test(actual)), `to match ${String(expected)}`),
    toHaveLength: (expected: number) => check((actual as { length?: number } | undefined)?.length === expected, `to have length ${expected}`),
    toBeGreaterThan: (expected: number) => check((actual as number) > expected, `to be greater than ${expected}`),
    toBeGreaterThanOrEqual: (expected: number) => check((actual as number) >= expected, `to be at least ${expected}`),
    toBeLessThan: (expected: number) => check((actual as number) < expected, `to be less than ${expected}`),
    toBeLessThanOrEqual: (expected: number) => check((actual as number) <= expected, `to be at most ${expected}`),
    toBeUndefined: () => check(actual === undefined, "to be undefined"),
    toHaveProperty: (name: string) => check(typeof actual === "object" && actual !== null && name in actual, `to have property ${name}`),
    toThrow: (expected?: RegExp | string) => {
      let thrown: unknown, threw = false
      try { (actual as () => unknown)() } catch (error) { threw = true; thrown = error }
      const text = thrown instanceof Error ? thrown.message : String(thrown)
      check(threw && (expected === undefined || (typeof expected === "string" ? text.includes(expected) : expected.test(text))), `to throw ${expected === undefined ? "" : String(expected)}`)
    },
  }
}

export function expect(actual: unknown, message?: string) {
  const base = matchers(actual, false, message)
  return {
    ...base,
    not: matchers(actual, true, message),
    rejects: {
      toThrow: async (expected?: RegExp | string) => {
        let thrown: unknown, threw = false
        try { await actual } catch (error) { threw = true; thrown = error }
        const text = thrown instanceof Error ? thrown.message : String(thrown)
        if (!threw || (expected !== undefined && !(typeof expected === "string" ? text.includes(expected) : expected.test(text)))) assert.fail(`${message ? `${message}: ` : ""}expected a rejection matching ${String(expected)}, got ${threw ? text : "a resolved value"}`)
      },
    },
  }
}
expect.stringContaining = (expected: string) => asymmetric(`stringContaining(${expected})`, (actual) => typeof actual === "string" && actual.includes(expected))
expect.stringMatching = (expected: RegExp | string) => asymmetric(`stringMatching(${String(expected)})`, (actual) => typeof actual === "string" && (typeof expected === "string" ? actual.includes(expected) : expected.test(actual)))

type Body = () => void | Promise<void>
export const describe = Object.assign(
  (name: string, body: () => void) => nodeDescribe(name, body),
  { each: (names: string[]) => (title: string, body: (name: string) => void) => { for (const name of names) nodeDescribe(title.replace("%s", name), () => body(name)) } },
)
export { afterEach, beforeEach }
export const test = (name: string, body: Body) => it(name, body)
export { it }
