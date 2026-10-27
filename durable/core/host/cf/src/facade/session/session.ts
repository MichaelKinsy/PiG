// The module the upstream session tests import by path (`../src/session/session.ts`): `new SessionImpl(storage)` over the core's
// Session mode (facade/session.ts opens it on first use). The gate replaces the reference's module of the same path with this one.
import type { Context } from "@earendil-works/chord"
import { createLazySession } from "../session.ts"
import type { SetTimer } from "../../timers.ts"
import type { Storage } from "../storage.ts"

type Session = {
  commit<T>(change: (tx: never) => T | Promise<T>, context: Context, options?: unknown): Promise<T>
  commitWith?<T>(change: (tx: never) => T | Promise<T>, context: Context, scope?: unknown, options?: unknown): Promise<T>
  [method: string]: unknown
}

export class SessionImpl {
  constructor(storage: Storage, now?: () => number, documentRetentionMs?: () => number | undefined, setTimer?: SetTimer, readOnly = false, persistWithinMs?: () => number | undefined) {
    const session = createLazySession(storage, { now, documentRetentionMs, setTimer, readOnly, persistWithinMs }) as Session
    // The descriptors, not the values: documentCacheSizes is a getter.
    return Object.defineProperties(this, { ...Object.getOwnPropertyDescriptors(session), commitWith: { value: (change: (tx: never) => unknown, context: Context, _scope?: unknown, commitOptions?: unknown) => session.commit(change as never, context, commitOptions), writable: true, configurable: true, enumerable: true } })
  }
}
/** session.ts createSession: the options are values, read live by the core as callbacks. */
export function createSession(storage: Storage, options: { now?: () => number; documentRetentionMs?: number; setTimer?: SetTimer; persistWithinMs?: number } = {}) {
  const { now, documentRetentionMs, setTimer, persistWithinMs } = options
  return createLazySession(storage, { now, setTimer, ...(documentRetentionMs === undefined ? {} : { documentRetentionMs: () => documentRetentionMs }), ...(persistWithinMs === undefined ? {} : { persistWithinMs: () => persistWithinMs }) })
}

export { documentRetention, persistWithin, setDefaultDocumentRetention } from "../agent.ts"

/** session.ts `contained`: run a host callback that must not fail its caller. A throw, or a promise it returns that rejects, goes to `onError`, which is not awaited. */
export function contained(callback: () => unknown, onError: (error: unknown) => void): void {
  try {
    const result = callback()
    // Any thenable, also a promise from another realm; `Promise.resolve` adopts it with handlers of its own.
    if (typeof (result as { then?: unknown } | null)?.then === "function") Promise.resolve(result).catch(onError)
  } catch (error) {
    onError(error)
  }
}
