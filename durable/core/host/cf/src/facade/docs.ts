// The built-in document tokens (harness/{live,inbox,usage,provider}.ts): the core owns their contents, hosts address them,
// and a host that opens one in a transaction computes its initial value and checkpoint verdict from the same definition.
import { uuidv7 } from "@earendil-works/pi-ai"
import { defineDoc, defineDocFamily } from "./documents.ts"

const conversationDoc = (kind: string, initial: () => any, checkpointWhen: (value: any) => boolean, extra: { version?: number; migrate?: (value: any) => any } = {}) =>
  defineDoc({ kind, version: extra.version ?? 1, scope: "conversation", history: "latest", fork: "initial", initial, checkpointWhen, ...(extra.migrate === undefined ? {} : { migrate: extra.migrate }) })

/** A copy of a version 1 compaction status or failure with `reason` renamed `trigger`, its keys in their order (live.ts renameReason). */
const renameReason = (value: Record<string, unknown>): Record<string, unknown> => Object.fromEntries(Object.entries(value).map(([key, field]) => [key === "reason" ? "trigger" : key, field]))

/** A complete base whenever nothing runs: no generation and no running tool slot (live.ts). Version 2: version 1 named a compaction's trigger `reason`, in `compactions` and `compactionFailure`. Version 3: versions 1 and 2 may lack `run.turns`; a run in flight across the upgrade counts its turns from 0. */
export const LiveDoc = conversationDoc("pi.live", () => ({}),
  (value: { generation?: unknown; tools?: { status?: string }[] }) => value.generation === undefined && !(value.tools ?? []).some(slot => slot.status === "running"),
  {
    version: 3,
    migrate: (value: { run?: Record<string, unknown>; compactions?: Record<string, unknown>[]; compactionFailure?: Record<string, unknown> }) => {
      const { run, compactions, compactionFailure } = value
      return {
        ...value,
        ...(run === undefined ? {} : { run: { ...run, turns: typeof run.turns === "number" ? run.turns : 0 } }),
        ...(compactions === undefined ? {} : { compactions: compactions.map(renameReason) }),
        ...(compactionFailure === undefined ? {} : { compactionFailure: renameReason(compactionFailure) }),
      }
    },
  })
/** A complete base exactly while the inbox is empty (inbox.ts). */
export const InboxDoc = conversationDoc("pi.inbox", () => ({ items: [] }), (value: { items: unknown[] }) => value.items.length === 0)
export const UsageDoc = conversationDoc("pi.usage", () => ({ models: {}, tools: {} }), () => true)
/** Every fork starts with a fresh identity instead of copying its parent (provider.ts). */
export const ProviderDoc = conversationDoc("pi.provider", () => ({ sessionId: uuidv7() }), () => true)

/** The nested calls of a tool call, a task-scoped family of the calling task keyed by the nested call's key (tool.ts NestedCallDoc). */
export const NestedCallDoc = defineDocFamily({ kind: "pi.tool.nested-call", version: 1, scope: "task", family: true, initial: () => ({}), checkpointWhen: () => false })
