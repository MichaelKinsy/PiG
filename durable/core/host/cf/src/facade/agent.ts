// Agent state, settings and resolution (src/harness/agent.ts). Resolution is host code in pi-durable, wraps included, so it
// stays in the host here: the core asks for it with the `resolveAgent` hook (PROTOCOL.md) and caches the answer per registry
// publication and stored state.
import { copyJson } from "@earendil-works/chord"
import { defineDoc } from "./documents.ts"
import type { HostExtension, HostRegistrySource, HostSection, HostTool } from "../registry.ts"

export const DEFAULT_RETRY_POLICY = { enabled: true, maxRetries: 3, baseDelayMs: 2000, maxAgentDelayMs: 60000 }
export const DEFAULT_COMPACTION_POLICY = { enabled: true, reserveTokens: 16384, keepRecentTokens: 20000, backgroundTokens: 32768 }
export const DEFAULT_PROGRESS_POLICY = { partialIntervalMs: 100, outputIntervalMs: 100 }
export const INSTRUCTIONS_KEY = "instructions"

export const AgentDoc = defineDoc({ kind: "pi.agent", version: 1, scope: "conversation", history: "rewindable", fork: "asOf", initial: () => ({}), checkpointWhen: () => true })

export type AgentState = { model?: { provider: string; modelId: string }; thinkingLevel?: string; extensions?: string[] | { add?: string[]; remove?: string[] }; tools?: string[] | { remove: string[] }; modelTools?: string[] | { remove?: string[]; add?: string[] }; instructions?: string; cwd?: string }
type Named = { readonly name: string }

/** Default `settings.contextRetentionMs`: ten minutes (harness/agent.ts). */
export const DEFAULT_CONTEXT_RETENTION_MS = 10 * 60 * 1000

/** Default document retention: ten minutes, as for kept contexts (session.ts defaultRetention). */
let defaultDocumentRetention = 600_000

/** Internal, for tests: the retention of every Session that keeps the default, as a suite run with eviction at once (session.ts setDefaultDocumentRetention). */
export function setDefaultDocumentRetention(ms: number): void {
  defaultDocumentRetention = ms
}

/** A retention as given, when it is a number of milliseconds, `0` or more (`Infinity` keeps documents); otherwise the default (session.ts documentRetention). */
export function documentRetention(ms: unknown): number {
  return typeof ms === "number" && ms >= 0 ? ms : defaultDocumentRetention
}

/** `settings.documentRetentionMs` as the core is told, which defaults to ten minutes: undefined when it equals the default. Infinity travels as the largest finite number. */
export function documentRetentionWire(settings: any): number | undefined {
  let ms: number
  try { ms = documentRetention(settings?.documentRetentionMs) } catch { ms = documentRetention(undefined) }
  return ms === 600_000 ? undefined : Number.isFinite(ms) ? ms : Number.MAX_VALUE
}

function documentRetentionEntry(settings: any): { documentRetentionMs?: number } {
  const ms = documentRetentionWire(settings)
  return ms === undefined ? {} : { documentRetentionMs: ms }
}

export function resolveSettings(settings: any) {
  return {
    ...(settings?.extensions === undefined ? {} : { extensions: settings.extensions }),
    stream: { ...settings?.stream },
    retry: { ...DEFAULT_RETRY_POLICY, ...settings?.retry },
    compaction: { ...DEFAULT_COMPACTION_POLICY, ...settings?.compaction },
    progress: {
      partialIntervalMs: settings?.progress?.partialIntervalMs ?? DEFAULT_PROGRESS_POLICY.partialIntervalMs,
      outputIntervalMs: settings?.progress?.outputIntervalMs ?? DEFAULT_PROGRESS_POLICY.outputIntervalMs,
    },
    contextRetentionMs: settings?.contextRetentionMs ?? DEFAULT_CONTEXT_RETENTION_MS,
    toolExecution: settings?.toolExecution ?? "parallel",
    steeringMode: settings?.steeringMode ?? "one-at-a-time",
    followUpMode: settings?.followUpMode ?? "one-at-a-time",
    ...documentRetentionEntry(settings),
  }
}

const names = (items: readonly Named[]) => items.map(i => i.name)
const isList = (v: unknown): v is readonly unknown[] => Array.isArray(v)

/** Apply one change to a `pi.agent` draft: a given field replaces the stored one, `null` clears it, `undefined` changes nothing. */
export function applyChange(state: Record<string, any>, change: any): void {
  const set = (key: string, value: unknown) => {
    if (value === undefined) return
    if (value === null) delete state[key]
    else state[key] = value
  }
  set("model", change.model === undefined || change.model === null ? change.model : { ...change.model })
  set("thinkingLevel", change.thinkingLevel)
  const e = change.extensions
  set("extensions", e === undefined || e === null ? e : isList(e) ? names(e as Named[]) : { ...(e.add === undefined ? {} : { add: names(e.add) }), ...(e.remove === undefined ? {} : { remove: names(e.remove) }) })
  const t = change.tools
  set("tools", t === undefined || t === null ? t : isList(t) ? names(t as Named[]) : { remove: names(t.remove) })
  // agent.ts applyChange: an object modelTools keeps its remove and add (add loads deferred tools).
  const m = change.modelTools
  set("modelTools", m === undefined || m === null ? m : isList(m) ? names(m as Named[]) : { ...(m.remove === undefined ? {} : { remove: names(m.remove) }), ...(m.add === undefined ? {} : { add: names(m.add) }) })
  set("instructions", change.instructions)
  set("cwd", change.cwd)
}

/** `configure(tx, conversationId, change)`: one change to `pi.agent` inside a commit. */
export async function configure(tx: { doc(token: unknown, ...args: unknown[]): Promise<any> }, conversationId: number, change: unknown): Promise<void> {
  applyChange(await tx.doc(AgentDoc, conversationId), change)
}

export type ResolvedAgent = { model?: unknown; thinkingLevel: string; extensions: HostExtension[]; tools: HostTool[]; callable: HostTool[]; deferred: HostTool[]; sections: HostSection[]; instructions?: string; cwd?: string }

/** Resolve an agent from its stored state, a registry snapshot and resolved settings. A wrapper that throws or renames drops its target and is reported. */
export function resolveAgent(state: AgentState | undefined, snapshot: ReturnType<HostRegistrySource["snapshot"]>, settings: ReturnType<typeof resolveSettings>, report: (e: unknown) => void): ResolvedAgent {
  const extensions = selectExtensions(state?.extensions, snapshot, settings)
  const composed = new Map<string, HostTool>()
  for (const x of extensions) for (const tool of x.tools ?? []) composed.set(tool.name, tool)
  const sections = new Map<string, HostSection>()
  for (const x of extensions) for (const s of x.sections ?? []) sections.set(s.key, s)
  for (const x of extensions) for (const wrap of x.wraps ?? []) {
    if ("tool" in wrap) applyWrap(composed, wrap.tool, t => wrap.wrap(t), t => t.name, report)
    else applyWrap(sections, wrap.section, s => wrap.wrap(s), s => s.key, report)
  }
  const enabled = filterTools([...composed.values()], state?.tools)
  const callableBy = (caller: "model" | "tools") => (tool: HostTool) => tool.callers === undefined || tool.callers.includes(caller)
  // agent.ts:221-228: the model is offered its callable tools as modelTools selects them; a deferred tool it is not offered
  // waits in `deferred` (tool_search loads it), and stays callable by tools.
  const modelCallable = enabled.filter(callableBy("model"))
  const tools = offeredTools(modelCallable, state?.modelTools)
  const offered = new Set(tools)
  const deferred = modelCallable.filter(tool => tool.deferred === true && !offered.has(tool))
  const callable = enabled.filter(callableBy("tools"))
  const instructions = state?.instructions
  const all = [...sections.values()]
  if (instructions !== undefined) all.push({ key: INSTRUCTIONS_KEY, render: () => instructions })
  return {
    ...(state?.model === undefined ? {} : { model: state.model }),
    thinkingLevel: state?.thinkingLevel ?? "off", extensions, tools, callable, deferred, sections: all,
    ...(instructions === undefined ? {} : { instructions }), ...(state?.cwd === undefined ? {} : { cwd: state.cwd }),
  }
}

/** agent.ts offeredTools: the tools offered to the model, as `AgentState.modelTools` describes. An array selects exactly its
 * names; otherwise every tool that is not deferred and not removed, then the loaded deferred tools in load order. */
function offeredTools(tools: HostTool[], filter: AgentState["modelTools"]): HostTool[] {
  if (Array.isArray(filter)) return filterTools(tools, filter)
  const removed = new Set(filter?.remove ?? [])
  const kept = tools.filter(tool => tool.deferred !== true && !removed.has(tool.name))
  const byName = new Map(tools.map(tool => [tool.name, tool]))
  const loaded = [...new Set(filter?.add ?? [])].flatMap(name => {
    const tool = byName.get(name)
    return tool?.deferred === true && !removed.has(name) ? [tool] : []
  })
  return [...kept, ...loaded]
}

/** agent.ts filterTools: `tools` through a stored filter: an array selects exactly its names, in its order; `{ remove }` drops names. */
function filterTools(tools: HostTool[], filter: AgentState["tools"] | readonly string[]): HostTool[] {
  if (filter === undefined) return tools
  if (Array.isArray(filter)) {
    const byName = new Map(tools.map(tool => [tool.name, tool]))
    return [...new Set(filter)].flatMap(name => { const tool = byName.get(name); return tool === undefined ? [] : [tool] })
  }
  const removed = new Set((filter as { remove: string[] }).remove)
  return tools.filter(tool => !removed.has(tool.name))
}

function selectExtensions(stored: AgentState["extensions"], snapshot: ReturnType<HostRegistrySource["snapshot"]>, settings: ReturnType<typeof resolveSettings>): HostExtension[] {
  let selected: string[]
  if (Array.isArray(stored)) selected = stored
  else {
    const base = (settings.extensions as Named[] | undefined)?.map(x => x.name) ?? snapshot.installed().map(x => x.name)
    const edit = stored as { add?: string[]; remove?: string[] } | undefined
    const removed = new Set(edit?.remove ?? [])
    selected = [...base, ...(edit?.add ?? [])].filter(n => !removed.has(n))
  }
  const byName = new Map(snapshot.installed().map(x => [x.name, x]))
  const out: HostExtension[] = []
  for (const name of new Set(selected)) { const x = byName.get(name); if (x) out.push(x) }
  return out
}

function applyWrap<T>(items: Map<string, T>, target: string, wrap: (i: T) => T, nameOf: (i: T) => string, report: (e: unknown) => void) {
  const item = items.get(target)
  if (item === undefined) return
  try {
    const wrapped = wrap(item)
    if (nameOf(wrapped) !== target) throw new Error(`Wrapper renamed ${target} to ${nameOf(wrapped)}`)
    items.set(target, wrapped)
  } catch (error) { items.delete(target); report(error) }
}

/** The `resolveAgent` hook answer: the resolved agent as JSON with handler indexes into the registry table (PROTOCOL.md). */
export function agentJSON(agent: ResolvedAgent, id: (object: unknown) => number) {
  // constrainedSampling rides the declaration the core puts in the system entry (prompt.ts toToolDeclaration).
  const toolJSON = (t: HostTool) => ({ name: t.name, description: t.description ?? "", parameters: t.parameters ?? {}, ...(t.constrainedSampling === undefined ? {} : { constrainedSampling: t.constrainedSampling }), replay: t.replay ?? "unsafe", handler: id(t), ...(t.executionMode === undefined ? {} : { executionMode: t.executionMode }), ...(t.outputLimits === undefined ? {} : { outputLimits: t.outputLimits }), ...(t.structuredOutputSchema === undefined ? {} : { structuredOutput: true }), ...(t.prepareArguments === undefined ? {} : { prepare: id(t.prepareArguments) }) })
  return {
    ...(agent.model === undefined ? {} : { model: agent.model }),
    thinkingLevel: agent.thinkingLevel,
    extensions: agent.extensions.map(x => x.name),
    tools: agent.tools.map(toolJSON),
    callable: agent.callable.map(toolJSON),
    sections: agent.sections.map(s => ({ key: s.key, tag: s.tag ?? true, handler: id(s) })),
    // The hooks of the selected extensions, in extension order (agent.ts agentHooks): hook name to handler index.
    hooks: agent.extensions.flatMap(x => (x.hooks ?? []).map(hook => ({ task: hook.task, handlers: Object.fromEntries(Object.entries(hook.handlers).filter(([, fn]) => typeof fn === "function").map(([name, fn]) => [name, id(fn)])) }))),
    ...(agent.instructions === undefined ? {} : { instructions: agent.instructions }),
    ...(agent.cwd === undefined ? {} : { cwd: agent.cwd }),
  }
}
export { copyJson }
