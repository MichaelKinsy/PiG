import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const scratch = fileURLToPath(new URL("./scratch.mjs", import.meta.url));

// Guard for the inventory tests' fixtures: a scratch directory must not outlive the process that made it, however that process ends. Each case runs in a private TMPDIR so the assertion sees only what the child created.
function withPrivateTmp(t) {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "pig-scratch-guard-"));
  t.after(() => fs.rmSync(tmp, { recursive: true, force: true }));
  return tmp;
}

function pigEntries(tmp) {
  return fs.readdirSync(tmp).filter((name) => name.startsWith("pig-"));
}

for (const [name, body, status] of [
  ["normal exit", "", 0],
  ["uncaught error", "throw new Error('boom');", 1],
  ["process.exit", "process.exit(3);", 3],
]) {
  test(`scratchDir removes its directories after ${name}`, (t) => {
    const tmp = withPrivateTmp(t);
    const script = `import { scratchDir } from ${JSON.stringify(scratch)};
      const a = scratchDir("pig-scratch-a-"); const b = scratchDir("pig-scratch-b-");
      console.log(a + "\\n" + b); ${body}`;
    const result = spawnSync(process.execPath, ["--input-type=module", "-e", script], { encoding: "utf8", env: { ...process.env, TMPDIR: tmp } });
    assert.equal(result.status, status, result.stderr);
    const [a, b] = result.stdout.trim().split("\n");
    assert.ok(a.startsWith(tmp) && b.startsWith(tmp), "directories are created under TMPDIR");
    assert.deepEqual(pigEntries(tmp), []);
  });
}

test("scratchDir removes its directories after SIGTERM", async (t) => {
  const tmp = withPrivateTmp(t);
  const script = `import { scratchDir } from ${JSON.stringify(scratch)};
    console.log(scratchDir("pig-scratch-sig-")); setInterval(() => {}, 1000);`;
  const child = spawn(process.execPath, ["--input-type=module", "-e", script], { env: { ...process.env, TMPDIR: tmp }, stdio: ["ignore", "pipe", "inherit"] });
  await new Promise((resolve) => child.stdout.once("data", resolve));
  assert.equal(pigEntries(tmp).length, 1);
  const closed = new Promise((resolve) => child.once("close", (code) => resolve(code)));
  child.kill("SIGTERM");
  assert.equal(await closed, 143);
  assert.deepEqual(pigEntries(tmp), []);
});
