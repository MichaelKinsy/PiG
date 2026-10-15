import { test } from "node:test"
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { closeSync, existsSync, mkdtempSync, openSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { spawnGroup } from "../lib/procs.mjs"

const exe = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "core", "host", "cf", "dist", "corenative")

// A native core that outgrows its bound ends itself with exit 3 and a report on stderr (durable/core/contracttest/guard), which
// the gate turns into a failed row; it does not run on until the host is out of memory. (A step that never returns is covered by
// the guard's own Go tests; here the real binary proves the wiring.)
test("corenative ends itself with a guard report when it is over its memory bound", { skip: !existsSync(exe) && "build it: sh durable/core/budget/build-native.sh" }, async () => {
	const dir = mkdtempSync(join(tmpdir(), "native-guard-"))
	const req = join(dir, "req"), res = join(dir, "res")
	execFileSync("mkfifo", [req, res])
	// Read-write opens do not wait for the core to open its ends.
	const held = [openSync(req, "r+"), openSync(res, "r+")]
	let stderr = ""
	const child = spawnGroup(exe, [req, res], { env: { ...process.env, DCORE_CORE_MEM_LIMIT_MB: "1" }, stdio: ["ignore", "ignore", "pipe"] })
	child.stderr.on("data", d => (stderr += d))
	const [code] = await new Promise(done => child.once("close", (c, s) => done([c, s])))
	for (const fd of held) closeSync(fd)
	rmSync(dir, { recursive: true, force: true })
	assert.equal(code, 3)
	assert.match(stderr, /corenative: guard: the Go runtime holds \d+ MiB, over the 1 MiB bound/)
})
