// createSession (src/session/session.ts): the tables-and-documents Session without the Harness. The core opens in `session`
// mode: no built-in documents, no scheduler.
import type { Context } from "@earendil-works/chord"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { Harness, type CoreSource } from "./harness.ts"
import type { Storage } from "./storage.ts"
import type { TxProxy } from "./tx.ts"

const NO_MODELS = {
  getModel: () => undefined,
  streamSimple: () => { throw new Error("a Session has no models") },
}
const NO_REGISTRY = { snapshot: () => ({ installed: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} }

export interface Session {
  commit<T>(change: (tx: TxProxy) => T | Promise<T>, context: Context): Promise<T>
  close(context: Context): Promise<void>
  subscribeCommits(listener: (publication: unknown, context: Context) => void): () => void
  subscribeClose(listener: () => void): () => void
  snapshot: Harness["snapshot"]
  snapshotAsOf: Harness["snapshotAsOf"]
  documentState: Harness["documentState"]
}

export async function createSession(storage: Storage, _context: Context = BACKGROUND_CONTEXT, options: { core?: CoreSource; sidecar?: "off" | "index" | "snapshot" } = {}): Promise<Session> {
  const harness = await Harness.open(storage, { models: NO_MODELS, registry: NO_REGISTRY, mode: "session", ...options }, BACKGROUND_CONTEXT)
  return {
    commit: (change, context) => harness.commit(change, context),
    close: context => harness.close(context),
    subscribeCommits: l => harness.subscribeCommits(l),
    subscribeClose: l => harness.subscribeClose(l),
    snapshot: harness.snapshot, snapshotAsOf: harness.snapshotAsOf, documentState: harness.documentState,
  }
}
