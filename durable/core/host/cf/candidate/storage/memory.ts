// MemoryStorage of the candidate: an in-memory store opened through the gate's capture, so every statement and commit is traced
// exactly like the reference's.
import type { Storage } from "@earendil-works/pi-durable"
import type { Store } from "@@HOST@@/store.ts"

type Contract = { openStore(path: string): Promise<Store & { close(): void }> }
const contract = () => (globalThis as { [k: symbol]: unknown })[Symbol.for("pig.contract")] as Contract

let count = 0
class CandidateMemoryStorage {
  private store?: Promise<Store & { close(): void }>
  open() { return this.store ??= contract().openStore(`:memory:${count++}`) }
  close() { void this.store?.then(s => s.close()) }
}
export const MemoryStorage = CandidateMemoryStorage as unknown as new () => Storage
