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

export type AgentState = { model?: { provider: string; modelId: string }; thinkingLevel?: string; extensions?: string[] | { add?: string[]; remove?: string[] }; tools?: string[] | { remove: string[] }; instructions?: string; cwd?: string }
type Named = { readonly name: string }

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
    toolExecution: settings?.toolExecution ?? "parallel",
    steeringMode: settings?.steeringMode ?? "one-at-a-time",
    followUpMode: settings?.followUpMode ?? "one-at-a-time",
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
  set("instructions", change.instructions)
  set("cwd", change.cwd)
}

/** `configure(tx, conversationId, change)`: one change to `pi.agent` inside a commit. */
export async function configure(tx: { doc(token: unknown, ...args: unknown[]): Promise<any> }, conversationId: number, change: unknown): Promise<void> {
  applyChange(await tx.doc(AgentDoc, conversationId), change)
}

export type ResolvedAgent = { model?: unknown; thinkingLevel: string; extensions: HostExtension[]; tools: HostTool[]; sections: HostSection[]; instructions?: string; cwd?: string }

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
  const filter = state?.tools
  let tools: HostTool[]
  if (filter === undefined) tools = [...composed.values()]
  else if (Array.isArray(filter)) { tools = []; for (const name of new Set(filter)) { const t = composed.get(name); if (t) tools.push(t) } }
  else { const removed = new Set(filter.remove); tools = [...composed.values()].filter(t => !removed.has(t.name)) }
  const instructions = state?.instructions
  const all = [...sections.values()]
  if (instructions !== undefined) all.push({ key: INSTRUCTIONS_KEY, render: () => instructions })
  return {
    ...(state?.model === undefined ? {} : { model: state.model }),
    thinkingLevel: state?.thinkingLevel ?? "off", extensions, tools, sections: all,
    ...(instructions === undefined ? {} : { instructions }), ...(state?.cwd === undefined ? {} : { cwd: state.cwd }),
  }
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
  return {
    ...(agent.model === undefined ? {} : { model: agent.model }),
    thinkingLevel: agent.thinkingLevel,
    extensions: agent.extensions.map(x => x.name),
    tools: agent.tools.map(t => ({ name: t.name, description: t.description ?? "", parameters: t.parameters ?? {}, replay: t.replay ?? "unsafe", handler: id(t), ...(t.executionMode === undefined ? {} : { executionMode: t.executionMode }), ...(t.outputLimits === undefined ? {} : { outputLimits: t.outputLimits }), ...(t.prepareArguments === undefined ? {} : { prepare: id(t.prepareArguments) }) })),
    sections: agent.sections.map(s => ({ key: s.key, tag: s.tag ?? true, handler: id(s) })),
    ...(agent.instructions === undefined ? {} : { instructions: agent.instructions }),
    ...(agent.cwd === undefined ? {} : { cwd: agent.cwd }),
  }
}
export { copyJson }
