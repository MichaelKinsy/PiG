// The module the upstream session tests import by path (`../src/session/session.ts`): `new SessionImpl(storage)` over the core's
// Session mode (facade/session.ts opens it on first use). The gate replaces the reference's module of the same path with this one.
import type { Context } from "@earendil-works/chord"
import { createLazySession } from "../session.ts"
import type { Storage } from "../storage.ts"

type Session = {
  commit<T>(change: (tx: never) => T | Promise<T>, context: Context): Promise<T>
  commitWith?<T>(change: (tx: never) => T | Promise<T>, context: Context): Promise<T>
  [method: string]: unknown
}

export class SessionImpl {
  constructor(storage: Storage, now?: () => number, documentRetentionMs?: () => number | undefined) {
    const session = createLazySession(storage, { now, documentRetentionMs }) as Session
    // The descriptors, not the values: documentCacheSizes is a getter.
    return Object.defineProperties(this, { ...Object.getOwnPropertyDescriptors(session), commitWith: { value: session.commit, writable: true, configurable: true, enumerable: true } })
  }
}
export const createSession = (storage: Storage) => createLazySession(storage)

export { documentRetention, setDefaultDocumentRetention } from "../agent.ts"

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
