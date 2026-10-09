// Storage handles for Harness.open: a synchronous SQL surface (ABI section 3) plus how to close it. The Durable Object host
// passes `doStorage(ctx.storage)`; Node programs pass MemoryStorage or openNodeSqliteStorage.
import { doStore } from "../store-do.ts"
import type { Store } from "../store.ts"

export interface Storage { open(): Promise<Store> | Store; close?(): void }

/** A Durable Object's SQL storage (`ctx.storage`). The Object owns its lifetime. */
export const openDurableObjectSqliteStorage = (storage: DurableObjectStorage): Storage => ({ open: () => doStore(storage) })

/** An in-memory SQLite database (node:sqlite), private to one Harness. */
export class MemoryStorage implements Storage {
  private store?: Store & { close(): void }
  async open() {
    const { nodeStore } = await import("../store-node.ts")
    return this.store ??= nodeStore(":memory:", { wal: false })
  }
  close() { this.store?.close() }
}

/** A SQLite file (node:sqlite), the store pi-durable's `openNodeSqliteStorage` opens. */
export function openNodeSqliteStorage(path: string): Storage {
  let store: (Store & { close(): void }) | undefined
  return {
    async open() { const { nodeStore } = await import("../store-node.ts"); return store ??= nodeStore(path) },
    close() { store?.close() },
  }
}
