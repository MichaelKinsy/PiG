// The nested-call half of src/harness/tool.ts: `api.executeTool(name, args, context, options)` admits another tool as a nested call of the
// running tool task (one commit: its pi.tool task owned by the caller, its slot in pi.live, its pi.tool.nested-call document), waits for the
// task and returns the result the nested call stored. The core runs the nested task; this is the caller's side.
import { copyJson, type Context, type JsonValue } from "@earendil-works/chord"
import { LiveDoc, NestedCallDoc } from "./facade/docs.ts"
import { ToolTask } from "./facade/define.ts"

/** The memo of a tool task that made a nested call, in any attempt (tool.ts MADE_NESTED_CALLS). */
const MADE_NESTED_CALLS = "pi.tool.madeNestedCalls"

type Runtime = {
  taskId: number
  conversationId: number
  commit<T>(change: (tx: any) => T | Promise<T>, context: Context): Promise<T>
  memo(name: string, candidate: unknown, context: Context): Promise<unknown>
  waitForTask(id: number, context: Context): Promise<any>
  snapshot(token: unknown, ...args: unknown[]): Promise<any>
}

export type NestedOptions = { key?: string; progress?: boolean }

/** tool.ts checkKey. */
function checkKey(key: string): void {
  if (key === "" || key.includes("/") || key === "__proto__" || /^[1-9][0-9]*$/.test(key)) {
    throw new Error(`Nested call key ${JSON.stringify(key)} must be non-empty, without "/", not "__proto__", and not a positive integer`)
  }
}

/** tool.ts jsonEqual: structural equality, key order does not matter. */
function jsonEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false
  if (Array.isArray(a) || Array.isArray(b)) {
    return Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((v, i) => jsonEqual(v, b[i]!))
  }
  const ka = Object.keys(a), kb = Object.keys(b)
  return ka.length === kb.length && ka.every(k => Object.hasOwn(b, k) && jsonEqual((a as any)[k], (b as any)[k]))
}

/** tool.ts fallbackResult: what the caller gets for a nested call without a stored result (the scheduler faulted or orphaned it). */
function fallbackResult(taskId: number, name: string, outcome: any) {
  const message = outcome.status === "faulted"
    ? `Tool ${name} failed: ${outcome.error.message}`
    : outcome.status === "orphaned" ? `Tool ${name} could not resume: ${outcome.reason}` : `Tool ${name} ended without a result`
  return { taskId, isError: true, diagnostics: [{ severity: "error", code: outcome.status, message }] }
}

/**
 * tool.ts executeTool for one tool invocation. `resumes` is the tool's replay rule (a safe tool reruns after a restart, so its nested
 * calls are kept; an unsafe tool abandons them). `admissions` collects the starts so the phase can wait for them before it settles.
 */
export function nestedExecutor(runtime: Runtime, parentCallId: string, resumes: boolean, assertLive: () => void, admissions: Promise<unknown>[]) {
  let sequence = 0
  let madeNestedMemo: Promise<unknown> | undefined
  return async (name: string, toolArgs: any, callContext: Context, options: NestedOptions = {}) => {
    assertLive()
    const key = options.key ?? String(++sequence)
    if (options.key !== undefined) checkKey(options.key)
    // The memo first, once per attempt; a failed one is tried again by the next call.
    madeNestedMemo ??= runtime.memo(MADE_NESTED_CALLS, true, callContext).catch((error: unknown) => { madeNestedMemo = undefined; throw error })
    const admission = madeNestedMemo.then(() => startNestedCall(runtime, parentCallId, name, toolArgs, { key, progress: options.progress, abandonOnRestart: !resumes }, callContext))
    admissions.push(admission)
    const id = await admission
    const settled = await runtime.waitForTask(id, callContext)
    const stored = (await runtime.snapshot(NestedCallDoc, runtime.taskId, key, callContext))?.result
    // A copy the tool may change; a nested call the scheduler faulted or orphaned stored none.
    return stored === undefined ? fallbackResult(id, name, settled.state.outcome) : copyJson(stored as JsonValue)
  }
}

/** tool.ts startNestedCall: nested call `key` of `parentCallId` in one commit. A key already made returns that call's task when it names the same tool and arguments. */
async function startNestedCall(runtime: Runtime, parentCallId: string, name: string, args: any, options: { key: string; progress: boolean | undefined; abandonOnRestart: boolean }, context: Context): Promise<number> {
  const key = options.key
  const call = { type: "toolCall", id: `${parentCallId}/${key}`, name, arguments: copyJson(args, { omitUndefinedProperties: true }) as any }
  let id: number | undefined
  await runtime.commit(async (tx: any) => {
    // Opened, or created empty for a key not made yet.
    const state = await tx.doc(NestedCallDoc, runtime.taskId, key, null)
    const existing = state.taskId
    if (existing !== undefined) {
      const input = (await tx.task(existing))?.input
      const same = input?.kind === "nested" && input.call.name === name && jsonEqual(input.call.arguments, call.arguments)
      if (!same) throw new Error(`Nested call ${call.id} was already made with another tool or other arguments`)
      id = existing
      return undefined
    }
    const input = { kind: "nested", parent: runtime.taskId, parentCallId, key, call, ...(options.progress === false ? { progress: false } : {}) }
    const created = await tx.createTask(ToolTask, input, { ownership: { kind: "task", taskId: runtime.taskId }, abandonOnRestart: options.abandonOnRestart })
    state.taskId = created
    const live = await tx.doc(LiveDoc, runtime.conversationId)
    live.nestedTools ??= []
    live.nestedTools.push({ callId: call.id, parentCallId, parentTaskId: runtime.taskId, name, taskId: created, arguments: call.arguments, status: "pending" })
    id = created
    return undefined
  }, context)
  return id!
}
