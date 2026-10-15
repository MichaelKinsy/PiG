// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

import type { Context } from "@earendil-works/chord"

/** Ports packages/durable/src/harness/util.ts Waiters: pending waits by key. Each settles once, through `resolve`, `rejectAll` or the abort of its context. The promise `add` returns is the waiter's own, so the caller's resumption spends exactly Pi's ticks. */
export class Waiters<K, T> {
  private readonly sets = new Map<K, Set<PromiseWithResolvers<T>>>()

  add(key: K, context: Context): Promise<T> {
    const signal = context.abortSignal
    if (signal?.aborted) return Promise.reject(signal.reason)
    const waiter = Promise.withResolvers<T>()
    const set = this.sets.get(key) ?? new Set()
    this.sets.set(key, set.add(waiter))
    if (signal !== undefined) {
      const onAbort = (): void => {
        set.delete(waiter)
        if (set.size === 0 && this.sets.get(key) === set) this.sets.delete(key)
        waiter.reject(signal.reason)
      }
      signal.addEventListener("abort", onAbort, { once: true })
      const detach = (): void => signal.removeEventListener("abort", onAbort)
      waiter.promise.then(detach, detach)
    }
    return waiter.promise
  }

  resolve(key: K, value: T): void {
    const set = this.sets.get(key)
    this.sets.delete(key)
    for (const waiter of set ?? []) waiter.resolve(value)
  }

  rejectAll(error: unknown): void {
    const sets = [...this.sets.values()]
    this.sets.clear()
    for (const set of sets) for (const waiter of set) waiter.reject(error)
  }
}
