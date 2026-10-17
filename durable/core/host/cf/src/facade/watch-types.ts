// The watch declarations of pi-durable's types.ts (WatchEnd, WatchHandle, JsonObject), kept structural so the facade does not
// import the package it replaces.
import type { Context } from "@earendil-works/chord"
import type { Op } from "@earendil-works/chord/delta"

export type JsonObject = { [key: string]: unknown }

export type WatchEnd =
  | { reason: "stopped" | "cancelled" | "session_closed" | "retired" }
  | { reason: "listener_error"; error: Error }
  | { reason: "session_failed"; error: unknown }

export interface WatchHandle<T> {
  readonly value: T
  start(listener: (value: T, ops: readonly Op[], context: Context) => Promise<void>): void
  stop(): Promise<WatchEnd>
  readonly closed: Promise<WatchEnd>
}
