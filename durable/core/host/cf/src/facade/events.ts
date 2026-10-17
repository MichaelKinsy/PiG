// watchEvents (src/harness/events.ts): the experimental agent event stream, built from the commits that touch one
// conversation. The core publishes a conversation's view as frames of ops plus the commit's entry, task and submission
// records (host/PROTOCOL.md, watches); `translate` turns each frame into events exactly as pi-durable does. Types are loose:
// the declarations are pi-durable's own (index.ts re-exports them).
import type { Context } from "@earendil-works/chord"
import { CommittedWatch } from "./observation.ts"
import type { Harness } from "./harness.ts"

type Json = any
type Op = any[]
type Changes = { entries: Json[]; tasks: Json[]; submissions: Json[] }

const AGENT_INITIAL = () => ({})
const USAGE_INITIAL = () => ({ models: {}, tools: {} })

function parts(view: Json) {
  return { live: (view.docs["pi.live"] ?? {}) as Json, inbox: view.docs["pi.inbox"] as Json, agent: view.docs["pi.agent"] as Json, usage: view.docs["pi.usage"] as Json }
}

function queued(inbox: Json): Json[] {
  return (inbox?.items ?? []).map((item: Json) => ({ id: item.id, mode: item.mode }))
}

function snapshotOf(view: Json): Json {
  const { live, inbox, agent, usage } = parts(view)
  return {
    type: "snapshot",
    entries: view.entries,
    ...(live.run === undefined ? {} : { run: { inputs: live.run.inputs } }),
    ...(live.generation === undefined ? {} : { generation: live.generation }),
    tools: live.tools ?? [],
    nestedTools: live.nestedTools ?? [],
    compactions: live.compactions ?? [],
    ...(live.compactionFailure === undefined ? {} : { compactionFailure: live.compactionFailure }),
    inbox: queued(inbox),
    agent: agent ?? AGENT_INITIAL(),
    usage: usage ?? USAGE_INITIAL(),
  }
}

export async function watchEvents(harness: Harness, conversationId: number, context: Context) {
  let watch!: CommittedWatch<readonly Json[]>
  let snapshot!: Json
  let held!: Set<number>
  await harness.attachView(conversationId, context, (initial: Json, release: () => void, completing?: readonly number[]) => {
    // The core read them on the view's line job; a view another observer already mounted has no such read to share.
    held = new Set<number>(completing ?? harness.completingGenerations(conversationId))
    let current = initial
    snapshot = snapshotOf(initial)
    watch = new CommittedWatch<readonly Json[]>([], release, error => harness.report(error), () => [snapshotOf(current)])
    return {
      advance: () => {},
      publication: (before: Json, after: Json, ops: readonly any[], changes: Changes | undefined, commitContext: Context) => {
        current = after
        const events = translate(before, after, ops, changes ?? { entries: [], tasks: [], submissions: [] }, held)
        if (events.length > 0) watch.advance(events, [], commitContext)
      },
      closeSession: failure => watch.closeSession(failure),
    }
  })
  const signal = context.abortSignal
  if (signal?.aborted) {
    watch.cancel()
    throw signal.reason
  }
  if (signal !== undefined) watch.observeCancellation(signal)
  return {
    snapshot,
    start: (listener: (events: readonly Json[], context: Context) => Promise<void>) => watch.start((events, _ops, deliveryContext) => listener(events, deliveryContext)),
    stop: () => watch.stop(),
    closed: watch.closed,
  }
}

function resultOf(entries: readonly Json[], callId: string): Json | undefined {
  return entries.find(entry => {
    const message = entry.model?.[0]
    return message?.role === "toolResult" && message.toolCallId === callId
  })
}

function translate(before: Json, after: Json, viewOps: readonly Op[], changes: Changes, held: Set<number>): Json[] {
  const entries: Json[] = changes.entries
  const tasks = new Map<number, Json>(changes.tasks.map(task => [task.id, task]))
  const submissions: Json[] = [...changes.submissions].sort((a, b) => a.id - b.id)
  // Nested results this commit stored, by nested task ID; read now, since the caller's documents may retire next (events.ts nestedResults).
  const nestedResults = new Map<number, Json>()
  for (const { value } of (changes as { nestedResults?: { key: string; value: Json }[] }).nestedResults ?? []) {
    if (value?.taskId !== undefined && value.result !== undefined) nestedResults.set(value.taskId, value.result)
  }
  if (viewOps.length === 0 && entries.length === 0 && tasks.size === 0 && submissions.length === 0) return []
  const was = parts(before)
  const now = parts(after)
  const events: Json[] = []

  const slotsBefore = new Map<string, Json>((was.live.tools ?? []).map((slot: Json) => [slot.callId, slot]))
  const slots: Json[] = now.live.tools ?? []
  const nestedBefore = new Map<number, Json>((was.live.nestedTools ?? []).map((slot: Json) => [slot.taskId, slot]))
  const nested: Json[] = now.live.nestedTools ?? []
  for (const slot of [...slots, ...nested]) {
    const previous = "parentCallId" in slot ? nestedBefore.get(slot.taskId) : slotsBefore.get(slot.callId)
    if (slot.status !== "running" || previous?.status === "running") continue
    // A nested slot carries the arguments the call runs with; a model-issued call's are in its intent checkpoint.
    const checkpoint = slot.taskId === undefined ? undefined : tasks.get(slot.taskId)?.state.checkpoint
    const args = "parentCallId" in slot ? slot.arguments : (checkpoint as { arguments?: Json } | undefined)?.arguments ?? {}
    events.push({ type: "tool_execution_start", ...callOf(slot), args })
  }
  const partialBefore = was.live.generation?.message
  const partial = now.live.generation?.message
  if (partial !== undefined && partialBefore === undefined) events.push({ type: "message_start", message: partial })
  else if (partial !== undefined && partial !== partialBefore) {
    events.push({ type: "message_update", usage: partial.usage, changes: messageChanges(viewOps, partial) })
  }
  for (const [index, slot] of slots.entries()) {
    const previous = slotsBefore.get(slot.callId)
    if (slot.status !== "running" || previous?.status !== "running") continue
    const update = toolUpdate(viewOps, ["tools", index], slot, previous)
    if (update === undefined) continue
    events.push({ type: "tool_execution_update", ...callOf(slot), ...update })
  }
  for (const [index, slot] of nested.entries()) {
    const previous = nestedBefore.get(slot.taskId)
    if (slot.status !== "running" || previous?.status !== "running") continue
    const update = toolUpdate(viewOps, ["nestedTools", index], slot, previous)
    if (update === undefined) continue
    events.push({ type: "tool_execution_update", ...callOf(slot), ...update })
  }
  const generation = now.live.generation
  const generationBefore = was.live.generation
  if (generation?.retry !== undefined && generationBefore?.retry === undefined) {
    const { at, error } = generation.retry
    events.push({ type: "auto_retry_start", attempt: generation.attempt, at, errorMessage: error })
  }
  if (generationBefore?.retry !== undefined && generation?.retry === undefined) {
    events.push({ type: "auto_retry_end", attempt: generationBefore.attempt })
  }
  if (generation?.deferred !== undefined && generation.deferred.pollAt !== generationBefore?.deferred?.pollAt) {
    events.push({ type: "deferred_poll", pollAt: generation.deferred.pollAt })
  }

  const toolEnds: Json[] = []
  const endTool = (slot: Json, entryId: number | undefined): void => {
    const entry = entries.find(candidate => candidate.id === entryId)
    toolEnds.push({ type: "tool_execution_end", ...callOf(slot), ...(entry === undefined ? {} : { entry }) })
  }
  for (const previous of slotsBefore.values()) {
    if (previous.status === "done") continue
    const slot = slots.find(candidate => candidate.callId === previous.callId)
    if (slot?.status === "done") endTool(slot, slot.entry)
    else if (slot === undefined) endTool(previous, resultOf(entries, previous.callId)?.id)
  }
  for (const slot of slots) {
    if (slot.status === "done" && !slotsBefore.has(slot.callId)) endTool(slot, slot.entry)
  }

  // Nested calls that end in this commit, the same way, children before the calls that made them: the lists hold parents first, so walk them backwards.
  const endNested = (slot: Json): void => {
    const result = nestedResults.get(slot.taskId)
    events.push({ type: "tool_execution_end", ...callOf(slot), ...(result === undefined ? {} : { result }) })
  }
  const nestedNow = new Map<number, Json>(nested.map(slot => [slot.taskId, slot]))
  for (const previous of [...nestedBefore.values()].reverse()) {
    if (previous.status === "done") continue
    const slot = nestedNow.get(previous.taskId)
    if (slot?.status === "done" || slot === undefined) endNested(slot ?? previous)
  }
  for (const slot of [...nested].reverse()) {
    if (slot.status === "done" && !nestedBefore.has(slot.taskId)) endNested(slot)
  }

  let assistantAppended = false
  for (const entry of entries) {
    events.push(...toolEnds.filter(end => end.entry === entry))
    const message = entry.model?.[0]
    if (message === undefined) {
      events.push({ type: "entry_appended", entry })
      continue
    }
    const streamed = message.role === "assistant" && partialBefore !== undefined && !assistantAppended
    if (message.role === "assistant") assistantAppended = true
    if (!streamed) events.push({ type: "message_start", message })
    events.push({ type: "message_end", entry })
  }
  events.push(...toolEnds.filter(end => end.entry === undefined))

  const compactionsBefore: Json[] = was.live.compactions ?? []
  const compactions: Json[] = now.live.compactions ?? []
  const compactionFailure = now.live.compactionFailure
  for (const { taskId, reason } of compactionsBefore) {
    if (compactions.some(status => status.taskId === taskId)) continue
    const error = compactionFailure?.taskId === taskId ? compactionFailure.error : undefined
    events.push({ type: "compaction_end", taskId, reason, ...(error === undefined ? {} : { error }) })
  }
  let turnEnded = false
  for (const task of tasks.values()) {
    const status = task.state.status
    if (task.kind === "pi.generation" && status === "completing" && !held.has(task.id)) {
      held.add(task.id)
      turnEnded = true
    }
    if (status !== "terminal") continue
    if (task.kind === "pi.generation" && !held.delete(task.id)) turnEnded = true
    const outcome = task.state.outcome
    if (outcome.status === "faulted" || outcome.status === "orphaned") {
      const message = outcome.status === "faulted" ? outcome.error.message : outcome.reason
      events.push({ type: "task_failed", taskId: task.id, kind: task.kind, message })
    }
  }
  if (turnEnded) events.push({ type: "turn_end" })
  const run = now.live.run
  const runBefore = was.live.run
  const runChanged = run?.inputs[0] !== runBefore?.inputs[0]
  if (runBefore !== undefined && runChanged) events.push({ type: "run_end", inputs: runBefore.inputs })

  for (const record of submissions) events.push({ type: "submission", record })
  if (now.inbox !== was.inbox) events.push({ type: "inbox_update", items: queued(now.inbox) })
  if (now.agent !== was.agent) events.push({ type: "agent_changed", agent: now.agent ?? AGENT_INITIAL() })
  if (now.usage !== was.usage) events.push({ type: "usage_changed", usage: now.usage ?? USAGE_INITIAL() })
  for (const { taskId, reason, blocking } of compactions) {
    if (!compactionsBefore.some(status => status.taskId === taskId)) events.push({ type: "compaction_start", taskId, reason, blocking })
  }
  if (run !== undefined && runChanged) events.push({ type: "run_start", inputs: run.inputs })
  if (run !== undefined && run.taskId !== runBefore?.taskId && tasks.get(run.taskId)?.kind === "pi.generation") {
    events.push({ type: "turn_start" })
  }
  return events
}

const PARTIAL_PATH = ["docs", "pi.live", "generation", "message"]

function messageChanges(viewOps: readonly Op[], message: Json): Json[] {
  const changes: Json[] = []
  const whole = new Set<number>()
  for (const op of viewOps) {
    const path = op[1] as (string | number)[]
    if (!startsWith(path, PARTIAL_PATH)) {
      if (startsWith(PARTIAL_PATH, path)) return [{ type: "message", message }]
      continue
    }
    const rest = path.slice(PARTIAL_PATH.length)
    if (rest[0] === "usage") continue
    if (rest[0] !== "content") return [{ type: "message", message }]
    if (rest.length === 1) {
      if (op[0] !== "p" || op[3] !== 0) return [{ type: "message", message }]
      for (const [offset, block] of (op[4] as Json[]).entries()) {
        const type = block.type === "text" ? "text_start" : block.type === "thinking" ? "thinking_start" : "toolcall_start"
        changes.push({ type, contentIndex: op[2] + offset, block })
      }
      continue
    }
    const contentIndex = rest[1] as number
    const field = rest[2]
    if (whole.has(contentIndex)) continue
    if (op[0] === "a" && rest.length === 3 && (field === "text" || field === "thinking")) {
      changes.push({ type: field === "text" ? "text_delta" : "thinking_delta", contentIndex, delta: op[2] })
    } else if (op[0] === "a" && field === "arguments") {
      changes.push({ type: "toolcall_delta", contentIndex, path: rest.slice(3), delta: op[2] })
    } else {
      whole.add(contentIndex)
      changes.push({ type: "block", contentIndex, block: message.content[contentIndex] })
    }
  }
  return changes
}

function toolUpdate(viewOps: readonly Op[], at: readonly ["tools" | "nestedTools", number], slot: Json, previous: Json): Json | undefined {
  const outputPath = ["docs", "pi.live", ...at, "output"]
  let trimStart = 0
  let append = ""
  let set = false
  for (const op of viewOps) {
    if (!startsWith(op[1] as (string | number)[], outputPath)) continue
    if (op[0] === "t") trimStart += op[2]
    else if (op[0] === "a") append += op[2]
    else set = true
  }
  let output: Json | undefined
  if (set || (slot.output !== previous.output && trimStart === 0 && append === "")) output = { set: slot.output ?? "" }
  else if (trimStart > 0 || append !== "") output = { ...(trimStart > 0 ? { trimStart } : {}), ...(append === "" ? {} : { append }) }
  const update = {
    ...(output === undefined ? {} : { output }),
    ...(slot.details === previous.details ? {} : { details: slot.details ?? null }),
    ...(slot.diagnostics === previous.diagnostics ? {} : { diagnostics: slot.diagnostics ?? [] }),
  }
  return Object.keys(update).length === 0 ? undefined : update
}

/** The identifying fields of a slot's tool events (events.ts callOf). */
function callOf(slot: Json): Json {
  return {
    toolCallId: slot.callId,
    toolName: slot.name,
    ...(slot.taskId === undefined ? {} : { taskId: slot.taskId }),
    ...("parentCallId" in slot ? { parentToolCallId: slot.parentCallId, parentTaskId: slot.parentTaskId } : {}),
  }
}

function startsWith(path: readonly (string | number)[], prefix: readonly (string | number)[]): boolean {
  return prefix.length <= path.length && prefix.every((segment, index) => path[index] === segment)
}
