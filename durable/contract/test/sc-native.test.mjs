// The SC row's native provider (CONTRACT 3.10): the core's write encoder behind a Storage. A broken encoder must fail the suite.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { runScRow } from "../lib/gate.mjs"

const repo = join(import.meta.dirname, "..", "..", "..")
const src = join(repo, "durable", "core", "contracttest", "cmd", "scprovider", "main.go")
const work = mkdtempSync(join(tmpdir(), "contract-sc-native-"))
test.after(() => rmSync(work, { recursive: true, force: true }))

function build(name, change) {
	const exe = join(work, `scprovider-${name}`)
	const args = ["build", "-o", exe]
	if (change) {
		const text = readFileSync(src, "utf8")
		assert.ok(text.includes(change[0]), `mutation ${name}: ${JSON.stringify(change[0])} is not in main.go`)
		const mutated = join(work, `main-${name}.go`)
		writeFileSync(mutated, text.replace(change[0], change[1]))
		const overlay = join(work, `overlay-${name}.json`)
		writeFileSync(overlay, JSON.stringify({ Replace: { [src]: mutated } }))
		args.push(`-overlay=${overlay}`)
	}
	args.push("./durable/core/contracttest/cmd/scprovider")
	execFileSync("go", args, { cwd: repo, env: { ...process.env, GOWORK: "" } })
	return exe
}

const row = { id: "SC" }

test("a commit that answers the wrong sequence fails the storage conformance suite", async () => {
	const impl = { name: "sc-mutant", vitest: false, scExe: build("seq", ["return float64(seq), nil", "return float64(seq + 1), nil"]) }
	const r = await runScRow(row, impl, { out: join(work, "out-mutant") })
	assert.equal(r.status, "fail")
	assert.match(JSON.stringify(r.detail), /cases failed/)
})

test("the core's encoder passes the cases the store implements; the only failure is ID-space exhaustion until the store rejects it", async () => {
	const impl = { name: "sc-native", vitest: false, scExe: build("ok") }
	const r = await runScRow(row, impl, { out: join(work, "out-ok") })
	if (r.status === "pass") return
	assert.equal(r.status, "fail")
	assert.deepEqual(r.detail.first, ["storage conformance keeps one global record ID namespace and rejects exhausted ID minting"])
})
