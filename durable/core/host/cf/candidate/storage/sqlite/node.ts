// openNodeSqliteStorage of the candidate (src/storage/sqlite/node.ts): the path opens through the gate's capture.
import type { Storage } from "@earendil-works/pi-durable"
import type { Store } from "@@HOST@@/store.ts"

type Contract = { openStore(path: string): Promise<Store & { close(): void }> }
const contract = () => (globalThis as { [k: symbol]: unknown })[Symbol.for("pig.contract")] as Contract

export async function openNodeSqliteStorage(path: string): Promise<Storage> {
  let store: Promise<Store & { close(): void }> | undefined
  return { open: () => (store ??= contract().openStore(path)), close: () => { void store?.then(s => s.close()) } } as unknown as Storage
}
