// A Storage commit that user code makes directly (a test seeding a store: `storage.commit(writes)` before a Harness opens it): the
// core, opened in `storage` mode over the store, applies the writes as it applies a Session commit (the SC row's provider,
// contract-sc-provider.ts). Node hosts with a core named by the environment only; the Harness's own commits never come here.
//
// The core session stays open across the direct commits of one Storage, as Pi's Storage keeps its state across commits: opening it loads
// the store's live records, so a fresh one per commit would make a run of n commits read O(n^2) rows. A commit another writer made to the
// store since (a Harness's core, another Storage over the same file) moved next_seq: the session's records may be stale, and it is opened
// again.
import { EVENT } from "../abi.ts"
import { CoreClient } from "../client.ts"
import { reviveError } from "../errors.ts"
import { HostSession, RejectedError } from "../host.ts"
import { WasmCore } from "../wasm.ts"
import type { Store } from "../store.ts"
import { coreFromEnvironment } from "./core-env.ts"

type Committed = { seqs: number[]; refused?: Error }

export class DirectCommits {
  private session?: { host: HostSession; client: CoreClient; nextSeq: number }
  private line: Promise<unknown> = Promise.resolve()
  private readonly store: Store
  constructor(store: Store) { this.store = store }

  /** Applies `batches` through the core; calls are served one at a time, in order. */
  commit(batches: readonly (readonly unknown[])[]): Promise<Committed> {
    const result = this.line.then(() => this.apply(batches))
    this.line = result.catch(() => {})
    return result
  }

  /** Closes the core session; a later commit opens another. */
  close(): void {
    const session = this.session
    this.session = undefined
    if (session) this.release(session)
  }

  private nextSeq(): number {
    // The core has not created the schema before the first commit.
    try { return Number(this.store.all("SELECT next_seq FROM durable_metadata", [])[0]?.[0] ?? 1) } catch { return 1 }
  }

  private release({ host, client }: { host: HostSession; client: CoreClient }): void {
    try { host.close() } catch { /* the handle may already be discarded */ }
    client.close()
  }

  private async open(): Promise<NonNullable<DirectCommits["session"]>> {
    const module = await coreFromEnvironment()
    if (!module) throw new Error("Storage.commit outside a Harness needs a core: set DCORE_WASM or DCORE_NATIVE")
    const core = module instanceof WebAssembly.Module ? new WasmCore(module) : module
    const host = new HostSession({ core: core.newSession(), sqlTable: core.sqlTable, store: this.store })
    const client = new CoreClient(host)
    try {
      const step = host.sendJSON(EVENT.open, { mode: "storage", settings: {}, models: [], registry: { extensions: [] }, sidecar: "off", delta: true })
      if (step?.status) throw new RejectedError(step.error?.name, step.error?.message, step.error?.raw ?? "")
    } catch (error) {
      this.release({ host, client })
      throw error
    }
    return { host, client, nextSeq: 0 }
  }

  private async apply(batches: readonly (readonly unknown[])[]): Promise<Committed> {
    if (this.session && this.session.nextSeq !== this.nextSeq()) this.close()
    const session = this.session ??= await this.open()
    try {
      const { seqs, refused } = await session.client.api<{ seqs: number[]; refused?: { name: string; message: string } }>("storage.commit", { batches })
      session.nextSeq = this.nextSeq()
      return { seqs, ...(refused === undefined ? {} : { refused: reviveError(refused) }) }
    } catch (error) {
      this.close()
      throw error
    }
  }
}
