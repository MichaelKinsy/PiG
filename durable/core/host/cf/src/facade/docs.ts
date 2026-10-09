// The built-in document tokens (kind, version, scope, history, fork): the core owns their contents, hosts address them.
import { defineDoc } from "./documents.ts"

const conversationDoc = (kind: string) => defineDoc({ kind, version: 1, scope: "conversation", history: "latest", fork: "initial", initial: () => ({}), checkpointWhen: () => true })
export const LiveDoc = conversationDoc("pi.live")
export const InboxDoc = conversationDoc("pi.inbox")
export const UsageDoc = conversationDoc("pi.usage")
export const ProviderDoc = conversationDoc("pi.provider")
