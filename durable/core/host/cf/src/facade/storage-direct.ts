// A Storage commit that user code makes directly (a test seeding a store: `storage.commit(writes)` before a Harness opens it): the
// core, opened in `storage` mode over the store, applies the writes as it applies a Session commit (the SC row's provider,
// contract-sc-provider.ts). Node hosts with a core named by the environment only; the Harness's own commits never come here.
import { EVENT } from "../abi.ts"
import { CoreClient } from "../client.ts"
import { HostSession, RejectedError } from "../host.ts"
import { WasmCore } from "../wasm.ts"
import type { Store } from "../store.ts"
import { coreFromEnvironment } from "./core-env.ts"

export async function directCommit(store: Store, writes: readonly unknown[]): Promise<number> {
  const module = await coreFromEnvironment()
  if (!module) throw new Error("Storage.commit outside a Harness needs a core: set DCORE_WASM or DCORE_NATIVE")
  const core = module instanceof WebAssembly.Module ? new WasmCore(module) : module
  const host = new HostSession({ core: core.newSession(), sqlTable: core.sqlTable, store })
  const client = new CoreClient(host)
  try {
    const step = host.sendJSON(EVENT.open, { mode: "storage", settings: {}, models: [], registry: { extensions: [] }, sidecar: "off", delta: true })
    if (step?.status) throw new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
    return Number(await client.api("storage.commit", { writes }))
  } finally {
    try { host.close() } catch { /* the handle may already be discarded */ }
    client.close()
  }
}
