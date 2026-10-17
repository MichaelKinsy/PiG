// The U and SC rows (CONTRACT section 6): the reference's own vitest suite, run from source with every @earendil-works
// package aliased to its src/ directory (the reference's config needs built pi-ai and chord), and, when CONTRACT_CANDIDATE
// names a directory laid out like packages/durable/src, with each of its modules replacing the reference's.
import { existsSync } from "node:fs"
import { join, resolve } from "node:path"

const pi = resolve(process.env.CONTRACT_PI_SOURCE)
const candidate = process.env.CONTRACT_CANDIDATE ? resolve(process.env.CONTRACT_CANDIDATE) : undefined
const durableSrc = join(pi, "packages", "durable", "src")
const pkg = { "pi-ai": "ai", chord: "chord", "pi-env": "env", "pi-agent-core": "agent", "pi-durable": "durable" }

const aliases = Object.entries(pkg).flatMap(([name, dir]) => [
	{ find: new RegExp(`^@earendil-works/${name}$`), replacement: join(pi, "packages", dir, "src", name === "chord" ? "index.ts" : "index.ts") },
	{ find: new RegExp(`^@earendil-works/${name}/(.*)$`), replacement: join(pi, "packages", dir, "src", "$1") },
])

/** A module of the reference's durable/src that the candidate replaces. */
const candidatePlugin = {
	name: "contract-candidate",
	enforce: "pre",
	async resolveId(source, importer, options) {
		if (!candidate) return null
		const resolved = await this.resolve(source, importer, { ...options, skipSelf: true })
		const id = resolved?.id?.split("?")[0]
		if (!id?.startsWith(durableSrc + "/")) return null
		const mine = join(candidate, id.slice(durableSrc.length + 1))
		return existsSync(mine) ? mine : null
	},
}

// A candidate that runs a core module compiles it before each file's tests (vitest.core-setup.mjs), as Pi loads its modules at import.
const setupFiles = [
	...(candidate && (process.env.DCORE_WASM || process.env.DCORE_NATIVE) ? [join(import.meta.dirname, "vitest.core-setup.mjs")] : []),
	...(process.env.CONTRACT_VITEST_TRACE ? [join(import.meta.dirname, "vitest.trace-setup.mjs")] : []),
]

// defineConfig is the identity function; a plain object keeps this file free of imports that only resolve inside the checkout.
export default {
	root: join(pi, "packages", "durable"),
	plugins: [candidatePlugin],
	// pool forks, 4 workers: vitest otherwise starts one worker per core of the host, which starves a shared machine (load 194 from
	// one reference run). The cap lives here so the gate, gate.sh and every manual run inherit it.
	test: { environment: "node", pool: "forks", maxWorkers: 4, ...(setupFiles.length ? { setupFiles } : {}), include: process.env.CONTRACT_TEST_INCLUDE ? process.env.CONTRACT_TEST_INCLUDE.split(",") : ["test/**/*.test.ts"] },
	resolve: { conditions: ["source"], alias: aliases },
	ssr: { resolve: { conditions: ["source"] } },
}
