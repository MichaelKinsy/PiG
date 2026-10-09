// The SC row (CONTRACT section 6): the upstream storage conformance suite over a Storage provider. The reference provider is
// pi-durable's SqliteStorage on a node:sqlite file; a candidate supplies `contract-sc-provider.ts` in its candidate_dir,
// exporting `withStorage` (the core's write encoder wrapped as a Storage, CONTRACT 3.10).
import { existsSync } from "node:fs"
import { join } from "node:path"
import { pathToFileURL } from "node:url"
import { mkdtemp, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { describe, expect, it } from "vitest"
import { registerStorageConformance } from "@earendil-works/pi-durable/testing"
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node"
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context"

const candidate = process.env.CONTRACT_CANDIDATE
const custom = candidate && join(candidate, "contract-sc-provider.ts")
const provider = custom && existsSync(custom)
	? (await import(pathToFileURL(custom).href)).withStorage
	: async (use: (storage: any) => Promise<void>) => {
		const dir = await mkdtemp(join(tmpdir(), "contract-sc-"))
		const storage = await openNodeSqliteStorage(join(dir, "store.sqlite"))
		try { await use(storage) } finally { await storage.close(BACKGROUND_CONTEXT); await rm(dir, { recursive: true, force: true }) }
	}
registerStorageConformance({ describe, expect, it } as never, "storage conformance", provider)
