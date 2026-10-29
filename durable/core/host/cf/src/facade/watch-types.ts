// The watch and observer declarations of pi-durable's types.ts (JsonObject, Observer, Observation), kept structural so the
// facade does not import the package it replaces. The watch types themselves are Chord's.
import type { Context, WatchCall, WatchEnd } from "@earendil-works/chord"

export type JsonObject = { [key: string]: unknown }

/** pi-durable's `Observer`: a synchronous observer of committed state, with the shapes of Chord's watch. `advance` only records the call; `end` is called once. */
export type Observer<T> = {
  advance(call: WatchCall<T>, context: Context): void
  end(end: WatchEnd): void
}

/** An attached `Observer`: the value at attachment, and how to detach it. */
export type Observation<T> = {
  readonly value: T
  /** Idempotent: no `advance` or `end` follows it. */
  detach(): void
}

/** pi-durable's `CommitObserver`: a synchronous observer of stored commits (`Session.observeCommits()`): `advance` gets each publication, `end` the Session's close. */
export type CommitObserver = {
  advance(publication: unknown, context: Context): void
  end(end: WatchEnd): void
}

/** An attached `CommitObserver`: how to detach it. Idempotent: no `advance` or `end` follows it. */
export type CommitObservation = { detach(): void }
