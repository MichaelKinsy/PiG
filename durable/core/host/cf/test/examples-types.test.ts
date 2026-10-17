// The 32 upstream examples run unmodified against the candidate API (CONTRACT section 6), so each export they use must have
// pi-durable's declaration. tools/check-examples-types.mjs type-checks them against the facade; any error in an example fails.
import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { dirname, join } from "node:path"
import { test } from "node:test"
import { fileURLToPath } from "node:url"

test("the upstream examples type-check against the facade", { timeout: 300_000 }, () => {
  const tool = join(dirname(fileURLToPath(import.meta.url)), "..", "tools", "check-examples-types.mjs")
  const run = spawnSync(process.execPath, [tool], { encoding: "utf8", env: process.env })
  assert.equal(run.status, 0, `${run.stdout}${run.stderr}`)
})
