import assert from "node:assert/strict"
import { execFileSync, spawnSync } from "node:child_process"
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import test from "node:test"
import { fileURLToPath } from "node:url"

const SETUP = join(dirname(fileURLToPath(import.meta.url)), "..", "tools", "setup.sh")
const PIN = readFileSync(join(dirname(SETUP), "..", "PIN"), "utf8").match(/^main\s+(\S+)/m)[1]

function makeStubTree(base, { createData = true } = {}) {
	const bin = join(base, "bin"), log = join(base, "npm.log"), dir = join(base, "pi"), old = join(base, "pi-1.0.4")
	mkdirSync(bin)
	// git: the checkout is already at the pin. npm: `npm ci` makes node_modules and records the call.
	writeFileSync(join(bin, "git"), `#!/bin/sh\n[ "$3" = rev-parse ] && echo ${PIN}\nexit 0\n`)
	writeFileSync(join(bin, "npm"), `#!/bin/sh\necho "$1" >>${JSON.stringify(log)}\nmkdir -p node_modules\n`)
	for (const f of ["git", "npm"]) chmodSync(join(bin, f), 0o755)
	mkdirSync(join(dir, ".git"), { recursive: true })
	mkdirSync(join(dir, "packages", "ai", "src", "providers"), { recursive: true })
	if (createData) mkdirSync(join(dir, "packages", "ai", "src", "providers", "data"), { recursive: true })
	for (const pkg of ["ai", "coding-agent", "durable"]) mkdirSync(join(old, "packages", pkg), { recursive: true })
	mkdirSync(join(old, "packages", "ai", "src", "providers"), { recursive: true })
	if (createData) mkdirSync(join(old, "packages", "ai", "src", "providers", "data"), { recursive: true })
	writeFileSync(join(dir, "package-lock.json"), "lock of the earlier pin\n")
	return { bin, log, dir, old }
}

test("setup: the reference's dependencies are reinstalled when its lock file changes (a cache installed for an earlier pin)", () => {
	const base = mkdtempSync(join(tmpdir(), "contract-setup-"))
	try {
		const { bin, log, dir, old } = makeStubTree(base)
		const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, CONTRACT_PI_SOURCE: dir, CONTRACT_PI_104_SOURCE: old, DURABLE_CONTRACT_CACHE: join(base, "cache") }
		const setup = () => execFileSync("sh", [SETUP], { env, encoding: "utf8" })
		const calls = () => (existsSync(log) ? readFileSync(log, "utf8").split("\n").filter(Boolean).length : 0)
		setup()
		assert.equal(calls(), 1, "a checkout without node_modules is installed")
		setup()
		assert.equal(calls(), 1, "an installed tree for the same lock file is kept")
		writeFileSync(join(dir, "package-lock.json"), "lock of the new pin\n")
		setup()
		assert.equal(calls(), 2, "a changed lock file reinstalls the tree")
	} finally { rmSync(base, { recursive: true, force: true }) }
})

test("setup: populates every imported catalog from the pinned vendored pi-ai pack without .upstream/current and repairs an incomplete cache", () => {
	const base = mkdtempSync(join(tmpdir(), "contract-setup-catalogs-"))
	try {
		const { bin, dir, old } = makeStubTree(base, { createData: false })
		writeFileSync(join(dir, "packages", "ai", "src", "providers", "all.ts"), "import modelDataManifest from \"./data/.manifest.json\" with { type: \"json\" };\n")
		writeFileSync(join(dir, "packages", "ai", "src", "providers", "qwen-token-plan-individual-cn.models.ts"), "import values from \"./data/qwen-token-plan-individual-cn.json\" with { type: \"json\" };\n")
		writeFileSync(join(dir, "packages", "ai", "src", "providers", "openai.models.ts"), "import values from \"./data/openai.json\" with { type: \"json\" };\n")
		writeFileSync(join(old, "packages", "ai", "src", "providers", "openai.models.ts"), "import values from \"./data/openai.json\" with { type: \"json\" };\n")
		const env = {
			...process.env,
			PATH: `${bin}:${process.env.PATH}`,
			CONTRACT_PI_SOURCE: dir,
			CONTRACT_PI_104_SOURCE: old,
			DURABLE_CONTRACT_CACHE: join(base, "cache"),
		}
		// 1. Fresh checkout (no providers/data dir, no .upstream/current): must unpack from the vendored pinned pi-ai pack with no network npm pack.
		const fresh = spawnSync("sh", [SETUP], { env, encoding: "utf8" })
		assert.equal(fresh.status, 0, `fresh setup failed: ${fresh.stderr}`)
		const dataDir = join(dir, "packages", "ai", "src", "providers", "data")
		const oldDataDir = join(old, "packages", "ai", "src", "providers", "data")
		assert.ok(existsSync(join(dataDir, "qwen-token-plan-individual-cn.json")), "post-1.0.4 catalog must be installed")
		assert.ok(existsSync(join(dataDir, "openai.json")), "openai catalog must be installed")
		assert.ok(existsSync(join(dataDir, ".manifest.json")), ".manifest.json must be installed")
		assert.ok(existsSync(join(oldDataDir, "openai.json")), "release worktree must receive the catalogs")
		assert.ok(existsSync(join(oldDataDir, "qwen-token-plan-individual-cn.json")), "release worktree receives the reference catalogs")

		// 2. Restored incomplete cache (providers/data exists, but lacks qwen-token-plan-individual-cn.json): setup must repair it.
		rmSync(join(dataDir, "qwen-token-plan-individual-cn.json"))
		rmSync(join(oldDataDir, "openai.json"))
		const repaired = spawnSync("sh", [SETUP], { env, encoding: "utf8" })
		assert.equal(repaired.status, 0, `repair setup failed: ${repaired.stderr}`)
		assert.ok(existsSync(join(dataDir, "qwen-token-plan-individual-cn.json")), "restored incomplete cache must be completed")
		assert.ok(existsSync(join(oldDataDir, "openai.json")), "restored incomplete release worktree must be completed")
	} finally { rmSync(base, { recursive: true, force: true }) }
})

test("setup: fails loudly naming the missing catalog when a provider module imports a catalog the pack lacks", () => {
	const base = mkdtempSync(join(tmpdir(), "contract-setup-missing-"))
	try {
		const { bin, dir, old } = makeStubTree(base, { createData: true })
		writeFileSync(join(dir, "packages", "ai", "src", "providers", "bogus-provider.models.ts"), "import values from \"./data/bogus-provider.json\" with { type: \"json\" };\n")
		const env = {
			...process.env,
			PATH: `${bin}:${process.env.PATH}`,
			CONTRACT_PI_SOURCE: dir,
			CONTRACT_PI_104_SOURCE: old,
			DURABLE_CONTRACT_CACHE: join(base, "cache"),
		}
		const res = spawnSync("sh", [SETUP], { env, encoding: "utf8" })
		assert.notEqual(res.status, 0, "setup must exit non-zero when an imported catalog is missing")
		assert.match(res.stderr, /bogus-provider\.json/, "stderr must name the missing catalog file")
	} finally { rmSync(base, { recursive: true, force: true }) }
})

test("setup: fails loudly when third_party/pi-packs manifest commit does not match durable/contract/PIN main", () => {
	const base = mkdtempSync(join(tmpdir(), "contract-setup-mismatch-"))
	try {
		const { bin, dir, old } = makeStubTree(base, { createData: false })
		const fakePacks = join(base, "pi-packs")
		mkdirSync(fakePacks, { recursive: true })
		writeFileSync(join(fakePacks, "manifest.json"), JSON.stringify({
			commit: "0000000000000000000000000000000000000000",
			packs: { "earendil-works-pi-ai-0.0.0.tgz": { name: "@earendil-works/pi-ai" } },
		}))
		const env = {
			...process.env,
			PATH: `${bin}:${process.env.PATH}`,
			CONTRACT_PI_SOURCE: dir,
			CONTRACT_PI_104_SOURCE: old,
			DURABLE_CONTRACT_CACHE: join(base, "cache"),
			CONTRACT_PI_PACKS_DIR: fakePacks,
		}
		const res = spawnSync("sh", [SETUP], { env, encoding: "utf8" })
		assert.notEqual(res.status, 0, "setup must exit non-zero when pi-packs manifest commit mismatches PIN main")
		assert.match(res.stderr, new RegExp(PIN), "stderr must name the expected PIN commit")
	} finally { rmSync(base, { recursive: true, force: true }) }
})
