// The built-in document tokens (harness/{live,inbox,usage,provider}.ts): the core owns their contents, hosts address them,
// and a host that opens one in a transaction computes its initial value and checkpoint verdict from the same definition.
import { uuidv7 } from "@earendil-works/pi-ai"
import { defineDoc, defineDocFamily } from "./documents.ts"

const conversationDoc = (kind: string, initial: () => any, checkpointWhen: (value: any) => boolean) =>
  defineDoc({ kind, version: 1, scope: "conversation", history: "latest", fork: "initial", initial, checkpointWhen })

/** A complete base whenever nothing runs: no generation and no running tool slot (live.ts). */
export const LiveDoc = conversationDoc("pi.live", () => ({}),
  (value: { generation?: unknown; tools?: { status?: string }[] }) => value.generation === undefined && !(value.tools ?? []).some(slot => slot.status === "running"))
/** A complete base exactly while the inbox is empty (inbox.ts). */
export const InboxDoc = conversationDoc("pi.inbox", () => ({ items: [] }), (value: { items: unknown[] }) => value.items.length === 0)
export const UsageDoc = conversationDoc("pi.usage", () => ({ models: {}, tools: {} }), () => true)
/** Every fork starts with a fresh identity instead of copying its parent (provider.ts). */
export const ProviderDoc = conversationDoc("pi.provider", () => ({ sessionId: uuidv7() }), () => true)

/** The nested calls of a tool call, a task-scoped family of the calling task keyed by the nested call's key (tool.ts NestedCallDoc). */
export const NestedCallDoc = defineDocFamily({ kind: "pi.tool.nested-call", version: 1, scope: "task", family: true, initial: () => ({}) })
