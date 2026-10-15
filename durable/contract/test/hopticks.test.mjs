// The per-backend tick table the hop model is checked against (tools/gen-hopticks.mjs) is what the reference's adapters spend now:
// a moved pin or a changed adapter regenerates a different table, and the test names the file to regenerate.
import assert from "node:assert/strict"
import { execFileSync } from "node:child_process"
import { join } from "node:path"
import test from "node:test"

test("durable/core/session/hopcost/testdata/ticks.json is what the reference's Storage adapters measure", () => {
	const out = execFileSync(process.execPath, ["--conditions=source", "--experimental-strip-types", "--no-warnings", join(import.meta.dirname, "..", "tools", "gen-hopticks.mjs"), "--check"], { encoding: "utf8" })
	assert.match(out, /ticks\.json is current/)
})
