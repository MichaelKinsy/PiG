// The SC row (durable/contract scenarios/sc.test.ts, CONTRACT 3.10): pi-durable's Storage interface served by the core's
// store. The core opens in `storage` mode and answers `storage.<method>` api requests: a commit runs the same store commit
// a Session commit does (Pi's statements, then the core's state), and every read comes from the core's state.
import { EVENT } from "../abi.ts"
import { CoreClient } from "../client.ts"
import { HostSession, RejectedError } from "../host.ts"
import { WasmCore } from "../wasm.ts"
import { coreFromEnvironment } from "./core-env.ts"
import { MemoryStorage } from "./storage.ts"

export async function withStorage(use: (storage: any) => Promise<void>): Promise<void> {
  const module = await coreFromEnvironment()
  if (!module) throw new Error("contract-sc-provider: DCORE_WASM or DCORE_NATIVE names the core to run")
  const core = module instanceof WebAssembly.Module ? new WasmCore(module) : module
  const backing = new MemoryStorage()
  const store = await backing.open()
  const host = new HostSession({ core: core.newSession(), sqlTable: core.sqlTable, store })
  const client = new CoreClient(host)
  const step = host.sendJSON(EVENT.open, { mode: "storage", settings: {}, models: [], registry: { extensions: [] }, sidecar: "off", delta: true })
  if (step?.status) throw new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
  const call = async (method: string, args: Record<string, unknown> = {}) => (await client.api(`storage.${method}`, args)) ?? undefined
  const storage = {
    commit: (writes: unknown[]) => call("commit", { writes }),
    mintId: () => call("mintId"),
    conversation: (id: number) => call("conversation", { id }),
    scanConversations: (query: unknown, limit: number, cursor?: unknown) => call("scanConversations", { query, limit, cursor }),
    entry: (...args: unknown[]) => typeof args[1] === "number" ? call("entry", { conversationId: args[0], id: args[1] }) : call("entry", { id: args[0] }),
    findLatestHeadMarker: (conversationId: number, atOrBefore: number | undefined) => call("findLatestHeadMarker", { conversationId, atOrBefore }),
    scanEntries: (query: unknown, limit: number, cursor?: unknown) => call("scanEntries", { query, limit, cursor }),
    task: (id: number) => call("task", { id }),
    scanTasks: (query: unknown, limit: number, cursor?: unknown) => call("scanTasks", { query, limit, cursor }),
    submission: (id: number) => call("submission", { id }),
    scanSubmissions: (query: unknown, limit: number, cursor?: unknown) => call("scanSubmissions", { query, limit, cursor }),
    submissionByRequest: (conversationId: number, requestId: string) => call("submissionByRequest", { conversationId, requestId }),
    findDocument: (address: unknown, at: unknown) => call("findDocument", { address, at }),
    document: (id: number, at: unknown) => call("document", { id, at }),
    scanDocuments: (query: unknown, limit: number, cursor?: unknown) => call("scanDocuments", { query, limit, cursor }),
    close: async () => { await call("close") },
  }
  try {
    await use(storage)
  } finally {
    try { host.close() } catch { /* the handle may already be discarded */ }
    client.close()
    backing.close()
  }
}
