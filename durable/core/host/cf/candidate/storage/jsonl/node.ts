// openNodeJsonlStorage of the candidate (src/storage/jsonl/node.ts). pi-durable keeps JSONL as a file format; the Durable core has
// one store format, so the gate opens a SQLite store under the directory (CONTRACT section 6, adaptation of the E rows).
import type { Context } from "@earendil-works/chord"
import type { Storage } from "@earendil-works/pi-durable"
import { join } from "node:path"
import { openNodeSqliteStorage } from "../sqlite/node.ts"

export async function openNodeJsonlStorage(directory: string, _context?: Context, _options?: object): Promise<Storage> {
  return openNodeSqliteStorage(join(directory, "jsonl-as-sqlite"))
}
