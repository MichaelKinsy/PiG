import { test } from "node:test"
import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const script = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "core", "contracttest", "gate.sh")

test("gate.sh defaults to the two shipped builds and rejects an unknown GATE_BUILDS entry before any stage", () => {
	const source = readFileSync(script, "utf8")
	assert.match(source, /builds=\$\{GATE_BUILDS:-"tinygo native"\}/)
	const bad = spawnSync("sh", [script], { env: { ...process.env, GATE_BUILDS: "tinygo bogus" }, encoding: "utf8" })
	assert.equal(bad.status, 2)
	assert.match(bad.stderr, /'bogus' is not tinygo, go or native/)
	assert.doesNotMatch(bad.stdout, /== 1 format/)
})
