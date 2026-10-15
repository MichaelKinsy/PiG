import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { killGroup, liveGroups, spawnGroup } from "../lib/procs.mjs"

const alive = pid => { try { process.kill(pid, 0); return true } catch { return false } }
const until = async (ok, ms = 5000) => { const end = Date.now() + ms; while (!ok()) { if (Date.now() > end) return false; await new Promise(r => setTimeout(r, 20)) } return true }

// The native core is a grandchild of the gate (gate -> node candidate -> corenative): killing only the child leaves it running.
test("killGroup ends a child's own children, which killing the child alone does not", async () => {
	const pidFile = join(tmpdir(), `procs-test-${process.pid}.pid`)
	const child = spawnGroup("sh", ["-c", `sleep 300 & echo $! > ${pidFile}; wait`], { stdio: "ignore" })
	assert.equal(liveGroups() > 0, true)
	assert.ok(await until(() => { try { return readFileSync(pidFile, "utf8").trim() !== "" } catch { return false } }), "the grandchild started")
	const grandchild = Number(readFileSync(pidFile, "utf8"))
	assert.ok(alive(grandchild))
	const closed = new Promise(res => child.once("close", res))
	killGroup(child)
	await closed
	assert.ok(await until(() => !alive(grandchild)), "the grandchild is gone with its group")
	assert.equal(liveGroups(), 0)
})

test("killGroup of a child that already exited is not an error", async () => {
	const child = spawnGroup("true", [], { stdio: "ignore" })
	await new Promise(res => child.once("close", res))
	killGroup(child)
})
