// The pi-durable public API over the Durable core. Upstream examples import `../../src/index.ts`; the corpus resolve hook aliases
// that specifier to this module, so they run unmodified (CONTRACT section 6, ADR D12).
//
// Every export has the declaration of @earendil-works/pi-durable: the implementations below are structurally looser (they take
// ids as numbers and documents as plain definitions), so each is cast to its upstream type here and nowhere else. A caller sees
// exactly pi-durable's types; `test/examples-types` compiles the 32 upstream examples against this module to prove it.
import type * as P from "@earendil-works/pi-durable"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import * as agent from "./agent.ts"
import * as define from "./define.ts"
import * as docs from "./docs.ts"
import * as documents from "./documents.ts"
import * as entries from "./entries.ts"
import { Harness as Core } from "./harness.ts"
import { createRegistry as createHostRegistry } from "./registry.ts"
import { watchEvents as hostWatchEvents } from "./events.ts"
import { createLazySession } from "./session.ts"
import * as storage from "./storage.ts"

export type * from "@earendil-works/pi-durable"
export { ConversationBusy, ReadAfterWrite, SessionFailed, StorageRequestError } from "../errors.ts"
export { ROOT_CONVERSATION_ID, useCore } from "./harness.ts"

export const AgentDoc = agent.AgentDoc as unknown as typeof P.AgentDoc
export const configure = agent.configure as unknown as typeof P.configure
export const DEFAULT_COMPACTION_POLICY = agent.DEFAULT_COMPACTION_POLICY as typeof P.DEFAULT_COMPACTION_POLICY
export const DEFAULT_PROGRESS_POLICY = agent.DEFAULT_PROGRESS_POLICY as typeof P.DEFAULT_PROGRESS_POLICY
export const DEFAULT_RETRY_POLICY = agent.DEFAULT_RETRY_POLICY as typeof P.DEFAULT_RETRY_POLICY

export const defineExtension = define.defineExtension as unknown as typeof P.defineExtension
export const defineTool = define.defineTool as unknown as typeof P.defineTool
export const defineTask = define.defineTask as unknown as typeof P.defineTask
export const hook = define.hook as unknown as typeof P.hook
export const section = define.section as unknown as typeof P.section
export const wrapSection = define.wrapSection as unknown as typeof P.wrapSection
export const wrapTool = define.wrapTool as unknown as typeof P.wrapTool
export const CompactionTask = define.CompactionTask as unknown as typeof P.CompactionTask
export const GenerationTask = define.GenerationTask as unknown as typeof P.GenerationTask
export const ToolTask = define.ToolTask as unknown as typeof P.ToolTask

export const InboxDoc = docs.InboxDoc as unknown as typeof P.InboxDoc
export const LiveDoc = docs.LiveDoc as unknown as typeof P.LiveDoc
export const NestedCallDoc = docs.NestedCallDoc as unknown as typeof P.NestedCallDoc
export const ProviderDoc = docs.ProviderDoc as unknown as typeof P.ProviderDoc
export const UsageDoc = docs.UsageDoc as unknown as typeof P.UsageDoc
export const defineDoc = documents.defineDoc as typeof P.defineDoc
export const defineDocFamily = documents.defineDocFamily as typeof P.defineDocFamily

export const defineEntry = entries.defineEntry as unknown as typeof P.defineEntry
export const AssistantEntry = entries.AssistantEntry as unknown as typeof P.AssistantEntry
export const CompactionEntry = entries.CompactionEntry as unknown as typeof P.CompactionEntry
export const ResetEntry = entries.ResetEntry as unknown as typeof P.ResetEntry
export const SystemEntry = entries.SystemEntry as unknown as typeof P.SystemEntry
export const ToolResultEntry = entries.ToolResultEntry as unknown as typeof P.ToolResultEntry
export const UserEntry = entries.UserEntry as unknown as typeof P.UserEntry

export const createRegistry = createHostRegistry as unknown as typeof P.createRegistry
export const MemoryStorage = storage.MemoryStorage as unknown as typeof P.MemoryStorage

export type Harness = P.Harness
export const Harness: typeof P.Harness = {
  open: (store, options, context = BACKGROUND_CONTEXT) => Core.open(store as never, options as never, context) as unknown as Promise<P.Harness>,
}

/** `createSession` opens lazily: it returns at once, as pi-durable's does, and each method waits for the core's cold open. */
export const createSession = ((store: P.Storage) => createLazySession(store as never)) as typeof P.createSession

/** Storage constructors of the subpath exports (`storage/sqlite/node`, `storage/cloudflare`): a store handle for `Harness.open`. */
export const openNodeSqliteStorage = (path: string) => Promise.resolve(storage.openNodeSqliteStorage(path) as unknown as P.Storage)
export const openDurableObjectSqliteStorage = (durableObjectStorage: DurableObjectStorage) => storage.openDurableObjectSqliteStorage(durableObjectStorage) as unknown as P.Storage

export const watchEvents = hostWatchEvents as unknown as typeof P.watchEvents
