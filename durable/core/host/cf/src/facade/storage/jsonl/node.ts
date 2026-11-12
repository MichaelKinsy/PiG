import type * as P from "@earendil-works/pi-durable"
import { openNodeJsonlStorage as open } from "../../storage.ts"

/** pi-durable's `storage/jsonl/node` entry: the store is a SQLite file in the directory, as the gate's reference stores it. */
export const openNodeJsonlStorage = (directory: string, _context?: unknown, options: { readOnly?: boolean } = {}) => Promise.resolve(open(directory, options) as unknown as P.Storage)
