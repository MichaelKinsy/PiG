// The vitest config's candidate plugin (scenarios/vitest.contract.config.mjs) replaces a reference module of packages/durable/src by the
// candidate's module of the same path. The reference's JsonlStorage is Pi's own class and drives its inner MemoryStorage through
// `prepareCommit`, so only it keeps the reference's storage/memory.ts.
import assert from "node:assert/strict"
import { join, resolve } from "node:path"
import test from "node:test"
import { pathToFileURL } from "node:url"
import { piSource } from "../lib/run.mjs"

const contract = resolve(import.meta.dirname, "..")
const facade = join(resolve(contract, "../.."), "durable/core/host/cf/src/facade")

test("the candidate plugin maps storage/memory.ts to the candidate's, except for the reference's JsonlStorage", async () => {
	const saved = { candidate: process.env.CONTRACT_CANDIDATE, pi: process.env.CONTRACT_PI_SOURCE }
	try {
		process.env.CONTRACT_PI_SOURCE = piSource()
		process.env.CONTRACT_CANDIDATE = facade
		const { default: config } = await import(`${pathToFileURL(join(contract, "scenarios/vitest.contract.config.mjs")).href}?plugin`)
		const plugin = config.plugins.find(p => p.name === "contract-candidate")
		const durableSrc = join(process.env.CONTRACT_PI_SOURCE, "packages", "durable", "src")
		const memory = join(durableSrc, "storage", "memory.ts")
		const ctx = { resolve: async () => ({ id: memory }) }
		const from = importer => plugin.resolveId.call(ctx, "../memory.ts", importer, {})
		assert.equal(await from(join(durableSrc, "session", "session.ts")), join(facade, "storage", "memory.ts"), "any other importer gets the candidate's")
		assert.equal(await from(join(durableSrc, "storage", "jsonl", "storage.ts")), null, "the reference's JsonlStorage keeps the reference's")
		assert.equal(await from(join(durableSrc, "storage", "sqlite", "storage.ts")), join(facade, "storage", "memory.ts"), "a sibling storage gets the candidate's")
	} finally {
		for (const [k, v] of [["CONTRACT_CANDIDATE", saved.candidate], ["CONTRACT_PI_SOURCE", saved.pi]]) if (v === undefined) delete process.env[k]; else process.env[k] = v
	}
})
