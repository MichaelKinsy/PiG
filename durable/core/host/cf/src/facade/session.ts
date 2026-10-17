// createSession (src/session/session.ts): the tables-and-documents Session without the Harness. pi-durable's returns a Session at
// once; the core's cold open is asynchronous, so the Session here opens on first use and every method waits for it. The core
// opens in `session` mode: no built-in documents, no scheduler.
import type { Context } from "@earendil-works/chord"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { Harness } from "./harness.ts"
import type { Storage } from "./storage.ts"

const NO_MODELS = {
  getModel: () => undefined,
  streamSimple: () => { throw new Error("a Session has no models") },
}
const NO_REGISTRY = { snapshot: () => ({ installed: () => [], extension: () => undefined, tools: () => [], sections: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} }

export function createLazySession(storage: Storage, options: { now?: () => number; documentRetentionMs?: () => number | undefined } = {}): unknown {
  let opened: Promise<Harness> | undefined
  // types.ts Session.closed: why the Session ended. A Session nobody used closes without opening the core.
  const ended = Promise.withResolvers<unknown>()
  let harness: Harness | undefined
  const open = () => {
    if (opened === undefined) {
      const { now, documentRetentionMs } = options
      // The retention is read live at each use, as Session reads its callback.
      const settings = documentRetentionMs === undefined ? undefined : { get documentRetentionMs() { return documentRetentionMs() } }
      opened = Harness.open(storage, { models: NO_MODELS, registry: NO_REGISTRY as never, mode: "session", ...(now === undefined ? {} : { now }), ...(settings === undefined ? {} : { settings }) }, BACKGROUND_CONTEXT)
      void opened.then(h => { harness = h })
      void opened.then(h => h.closed).then(r => ended.resolve(r), () => {})
    }
    return opened
  }
  // Listeners subscribed before the core is open attach when it opens; the returned function removes them either way.
  const later = <L>(subscribe: (h: Harness, listener: L) => () => void) => (listener: L) => {
    let off: (() => void) | undefined
    let cancelled = false
    void open().then(h => { if (!cancelled) off = subscribe(h, listener) }, () => {})
    return () => { cancelled = true; off?.() }
  }
  const via = (name: "snapshot" | "snapshotAsOf" | "documentState" | "watchDoc" | "unloadDocuments") => async (...args: unknown[]) => (((await open()) as unknown as Record<string, (...a: unknown[]) => unknown>)[name]!)(...args)
  return {
    closed: ended.promise,
    /** Internal: run a read-only job on the mutation line (session.ts readOnLine): a commit that writes nothing holds the line for it. */
    readOnLine: async <T>(job: () => Promise<T>): Promise<T> => (await open()).readOnLine(job),
    /** Internal, for tests: the document cache sizes the core last reported. */
    get documentCacheSizes() { return harness?.documentCacheSizes ?? { cached: 0, idle: 0, attached: 0, sweeps: 0 } },
    commit: async <T>(change: (tx: never) => T | Promise<T>, context: Context): Promise<T> => (await open()).commit(change as never, context),
    // Closing a Session nobody used must not cold-open the core just to seal it.
    close: async (context: Context) => { if (opened) await (await opened).close(context); else ended.resolve({ reason: "closed" }) },
    subscribeCommits: later<(publication: unknown, context: Context) => void>((h, l) => h.subscribeCommits(l)),
    subscribeClose: later<() => void>((h, l) => h.subscribeClose(l)),
    snapshot: via("snapshot"), snapshotAsOf: via("snapshotAsOf"), documentState: via("documentState"), watchDoc: via("watchDoc"), unloadDocuments: via("unloadDocuments"),
  }
}
