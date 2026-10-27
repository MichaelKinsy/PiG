// The watch and observer declarations of pi-durable's types.ts (JsonObject, Observer, Observation), kept structural so the
// facade does not import the package it replaces. The watch types themselves are Chord's.
import type { Context, WatchChange } from "@earendil-works/chord"

export type JsonObject = { [key: string]: unknown }

/** pi-durable's `Observer`: a synchronous observer of committed state. `advance` only records the change; `end` is called once. */
export type Observer<T> = {
  advance(change: WatchChange<T>, context: Context): void
  end(failure: { readonly error: unknown } | undefined): void
}

/** An attached `Observer`: the value at attachment, and how to detach it. */
export type Observation<T> = {
  readonly value: T
  /** Idempotent: no `advance` or `end` follows it. */
  detach(): void
}
