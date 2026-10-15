// The behavior tests of Pi's packages/codemode/test/sandbox.test.ts, as ported to Go (codemode/sandbox_test.go), run against the
// Cloudflare Worker host: a Go RemoteSandbox whose remote is the TinyGo codemode module and QuickJS served by src/codemode/serve.ts
// in a Node process (test/worker/codemode-stdio.ts). -count=1: the module is an input go test cannot see. This is the Worker path against every behavior the worker, inline and remote
// backends are held to.
import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { join } from "node:path"
import { test } from "node:test"
import { codemodeWasmPath } from "./codemode-helpers.ts"
import { root } from "./helpers.ts"
import { tempDir } from "./helpers.ts"

test("every sandbox.test.ts behavior test passes on the Worker host backend", { timeout: 1_800_000 }, () => {
  const repo = join(root, "..", "..", "..", "..")
  const agentDir = tempDir("codemode-go")
  const result = spawnSync("go", ["test", "-count=1", "-tags", "codemodeworker", "-run", "TestSandboxBehaviorOnTheWorkerHostBackend", "./codemode"], {
    cwd: repo,
    encoding: "utf8",
    env: { ...process.env, CODEMODE_WORKER_WASM: codemodeWasmPath(), PIG_CODING_AGENT_DIR: agentDir, PI_CODING_AGENT_DIR: agentDir, PIG_HOME: agentDir, PI_HOME: agentDir },
  })
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`)
})
