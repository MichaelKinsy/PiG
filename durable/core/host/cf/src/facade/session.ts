// createSession (src/session/session.ts): the tables-and-documents Session without the Harness. pi-durable's returns a Session at
// once; the core's cold open is asynchronous, so the Session here opens on first use and every method waits for it. The core
// opens in `session` mode: no built-in documents, no scheduler.
import type { Context } from "@earendil-works/chord"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"
import { Harness } from "./harness.ts"
import type { SetTimer } from "../timers.ts"
import type { Storage } from "./storage.ts"

const NO_MODELS = {
  getModel: () => undefined,
  streamSimple: () => { throw new Error("a Session has no models") },
}
const NO_REGISTRY = { snapshot: () => ({ installed: () => [], extension: () => undefined, tools: () => [], sections: () => [], task: () => undefined, tasks: () => [] }), subscribe: () => () => {} }

/** The Session each running watch references (session.ts: a watch's closures hold the Session), dropped once the watch is stopped or ended. */
const watchOwners = new WeakMap<object, object>()

/**
 * The `watchDoc` of a Session: its watch references `owner`, the object the caller holds as the Session, until it is stopped or ends.
 * A function of its own, so that no closure the Harness holds (V8 shares one context among the closures of a scope) captures `owner`.
 */
function watchingVia(open: () => Promise<Harness>, owner: object) {
  return async (...args: unknown[]) => {
    const watch = await (await open()).watchDoc(...(args as Parameters<Harness["watchDoc"]>)) as { closed: Promise<unknown> } | undefined
    if (watch !== undefined) { watchOwners.set(watch, owner); void watch.closed.then(() => watchOwners.delete(watch)) }
    return watch
  }
}

/**
 * HarnessOptions.onReport of a Session: the report goes to the `report(error, info)` the owner defines (session.ts: a Session subclass
 * overrides it, the plain one drops reports). The owner is held weakly, so the Harness keeps nothing of the object that holds it.
 */
function reportingTo(owner: object): (error: unknown, info?: unknown) => void {
  const ref = new WeakRef(owner as { report?: (error: unknown, info?: unknown) => void })
  return (error, info) => { ref.deref()?.report?.(error, info) }
}

/** `owner` is the object the caller holds as the Session (SessionImpl's `this`); a bare Session is its own owner. */
export function createLazySession(storage: Storage, options: { now?: () => number; documentRetentionMs?: () => number | undefined; setTimer?: SetTimer; readOnly?: boolean; persistWithinMs?: () => number | undefined } = {}, owner: object = {}): unknown {
  let opened: Promise<Harness> | undefined
  // types.ts Session.closed: why the Session ended. A Session nobody used closes without opening the core.
  const ended = Promise.withResolvers<unknown>()
  let harness: Harness | undefined
  const reported = reportingTo(owner)
  const open = () => {
    if (opened === undefined) {
      const { now, documentRetentionMs, setTimer, readOnly, persistWithinMs } = options
      // The retention and the default window of a deferred commit are read live at each use, as Session reads its callbacks.
      // Live getters (a spread would read them once).
      let settings: Record<string, unknown> | undefined
      if (documentRetentionMs !== undefined || persistWithinMs !== undefined) {
        settings = {}
        if (documentRetentionMs !== undefined) Object.defineProperty(settings, "documentRetentionMs", { get: () => documentRetentionMs(), enumerable: true })
        if (persistWithinMs !== undefined) Object.defineProperty(settings, "progress", { value: Object.defineProperty({}, "persistWithinMs", { get: () => persistWithinMs(), enumerable: true }), enumerable: true })
      }
      opened = Harness.open(storage, { models: NO_MODELS, registry: NO_REGISTRY as never, mode: "session", onReport: reported, ...(now === undefined ? {} : { now }), ...(settings === undefined ? {} : { settings }), ...(setTimer === undefined ? {} : { setTimer }), ...(readOnly === true ? { readOnly } : {}) } as never, BACKGROUND_CONTEXT)
      void opened.then(h => { harness = h })
      void opened.then(h => h.closed).then(r => ended.resolve(r), () => {})
    }
    return opened
  }
  // Listeners subscribed before the core is open attach when it opens; the returned function removes them either way.
  const later = <L>(subscribe: (h: Harness, listener: L) => () => void) => (listener: L) => {
    // An open Session takes the listener at once, as Pi's does: a commit adopted in this very tick (a Storage's step after `adopt`) sees it.
    if (harness !== undefined) return subscribe(harness, listener)
    let off: (() => void) | undefined
    let cancelled = false
    void open().then(h => { if (!cancelled) off = subscribe(h, listener) }, () => {})
    return () => { cancelled = true; off?.() }
  }
  const via = (name: "snapshot" | "snapshotAsOf" | "documentState" | "observeDoc" | "unloadDocuments") => async (...args: unknown[]) => (((await open()) as unknown as Record<string, (...a: unknown[]) => unknown>)[name]!)(...args)
  const watchDoc = watchingVia(open, owner)
  const methods = {
    closed: ended.promise,
    /** Internal: run a read-only job on the mutation line (session.ts readOnLine): a commit that writes nothing holds the line for it. */
    readOnLine: async <T>(job: () => Promise<T>): Promise<T> => (await open()).readOnLine(job),
    /** Internal, for tests: the document cache sizes the core last reported. */
    get documentCacheSizes() { return harness?.documentCacheSizes ?? { cached: 0, idle: 0, attached: 0, sweeps: 0, held: 0 } },
    commit: async <T>(change: (tx: never) => T | Promise<T>, context: Context, commitOptions?: unknown): Promise<T> => (await open()).commit(change as never, context, commitOptions as never),
    // Closing a Session nobody used must not cold-open the core just to seal it.
    close: async (context: Context) => { if (opened) await (await opened).close(context); else ended.resolve({ reason: "closed" }) },
    /** Internal (session.ts observeCommits): the Session components' commit listener, run before the host's `subscribeCommits` listeners. */
    observeCommits: later<(publication: unknown, context: Context) => void>((h, l) => h.observeCommits(l)),
    /** Internal, for tests (session.ts fail): fail the Session. */
    fail: (error: unknown) => { if (harness !== undefined) harness.fail(error); else void open().then(h => h.fail(error), () => {}) },
    subscribeCommits: later<(publication: unknown, context: Context) => void>((h, l) => h.subscribeCommits(l)),
    subscribeClose: later<() => void>((h, l) => h.subscribeClose(l)),
    snapshot: via("snapshot"), snapshotAsOf: via("snapshotAsOf"), documentState: via("documentState"), watchDoc, observeDoc: via("observeDoc"), unloadDocuments: via("unloadDocuments"),
  }
  // The descriptors, not the values: documentCacheSizes is a getter.
  return Object.defineProperties(owner, Object.getOwnPropertyDescriptors(methods))
}
