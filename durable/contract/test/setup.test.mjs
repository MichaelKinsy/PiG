import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import test from "node:test"
import { fileURLToPath } from "node:url"

const SETUP = join(dirname(fileURLToPath(import.meta.url)), "..", "tools", "setup.sh")
const PIN = readFileSync(join(dirname(SETUP), "..", "PIN"), "utf8").match(/^main\s+(\S+)/m)[1]
const TICKS = readFileSync(join(dirname(SETUP), "..", "PIN"), "utf8").match(/^ticks\s+(\S+)/m)[1]

test("setup: the reference's dependencies are reinstalled when its lock file changes (a cache installed for an earlier pin)", () => {
	const base = mkdtempSync(join(tmpdir(), "contract-setup-"))
	try {
		const bin = join(base, "bin"), log = join(base, "npm.log"), dir = join(base, "pi"), old = join(base, "pi-1.0.4"), ticks = join(base, "pi-ticks")
		mkdirSync(bin)
		// git: the checkout is already at the pin. npm: `npm ci` makes node_modules and records the call.
		writeFileSync(join(bin, "git"), `#!/bin/sh\n[ "$3" = rev-parse ] && { case "$2" in *pi-ticks*) echo ${TICKS} ;; *) echo ${PIN} ;; esac; }\nexit 0\n`)
		writeFileSync(join(bin, "npm"), `#!/bin/sh\necho "$1" >>${JSON.stringify(log)}\nmkdir -p node_modules\n`)
		for (const f of ["git", "npm"]) chmodSync(join(bin, f), 0o755)
		mkdirSync(join(dir, ".git"), { recursive: true })
		mkdirSync(join(ticks, "packages", "ai", "src", "providers", "data"), { recursive: true })
		writeFileSync(join(ticks, "package-lock.json"), "ticks lock\n")
		mkdirSync(join(dir, "packages", "ai", "src", "providers", "data"), { recursive: true })
		for (const pkg of ["ai", "coding-agent", "durable"]) mkdirSync(join(old, "packages", pkg), { recursive: true })
		mkdirSync(join(old, "packages", "ai", "src", "providers", "data"), { recursive: true })
		const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, CONTRACT_PI_SOURCE: dir, CONTRACT_PI_104_SOURCE: old, CONTRACT_PI_TICKS_SOURCE: ticks, DURABLE_CONTRACT_CACHE: join(base, "cache") }
		const setup = () => execFileSync("sh", [SETUP], { env, encoding: "utf8" })
		const calls = () => (existsSync(log) ? readFileSync(log, "utf8").split("\n").filter(Boolean).length : 0)
		writeFileSync(join(dir, "package-lock.json"), "lock of the earlier pin\n")
		setup()
		assert.equal(calls(), 2, "a checkout without node_modules is installed, the main reference and the tick reference")
		setup()
		assert.equal(calls(), 2, "an installed tree for the same lock file is kept")
		writeFileSync(join(dir, "package-lock.json"), "lock of the new pin\n")
		setup()
		assert.equal(calls(), 3, "a changed lock file reinstalls the tree")
	} finally { rmSync(base, { recursive: true, force: true }) }
})
