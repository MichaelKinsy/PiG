import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";

const [side, temp] = process.argv.slice(2);
assert.ok(side === "pig" || side === "pi");
assert.ok(temp, "scenario-owned temporary root is required");
const root = mkdtempSync(join(temp, "startup-metadata-"));
try {
  const binary = side === "pig" ? join(root, "pig") : process.execPath;
  const piRoot = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
  if (side === "pi") assert.equal(JSON.parse(readFileSync(join(piRoot, "package.json"), "utf8")).version, "0.87.1", "pinned Pi package");
  const prefix = side === "pig" ? [] : [join(piRoot, "dist/cli.js")];
  if (side === "pig") execFileSync("go", ["build", "-o", binary, "./cmd/pig"], { stdio: "pipe" });
  const records = path => readFileSync(path, "utf8").trim().split("\n").map(line => JSON.parse(line));
  const names = path => records(path).filter(entry => entry.type === "session_info").map(entry => entry.name);
  const outcomes = [];
  for (const mode of ["resume", "fork"]) {
    const cwd = join(root, mode), agent = join(cwd, "agent"), sessions = join(cwd, "sessions");
    for (const dir of [cwd, agent, sessions]) mkdirSync(dir, { recursive: true });
    const source = join(cwd, "source.jsonl");
    const stamp = "2026-01-01T00:00:00.000Z";
    writeFileSync(source, [
      { type: "session", version: 3, id: "existing-session", timestamp: stamp, cwd },
      { type: "message", id: "assistant-1", parentId: null, timestamp: stamp, message: { role: "assistant", content: [{ type: "text", text: "hello" }], provider: "anthropic", model: "claude-sonnet-4-5", timestamp: Date.parse(stamp) } },
    ].map(entry => JSON.stringify(entry)).join("\n") + "\n");
    const args = mode === "resume" ? ["--session", source] : ["--fork", source, "--session-id", "chosen-fork-id", "--session-dir", sessions];
    const result = spawnSync(binary, [...prefix, ...args, "--name", "  CLI Named Session  ", "--model", "missing-model", "-p", "hi"], {
      cwd, env: { ...process.env, HOME: cwd, PIG_CODING_AGENT_DIR: agent, PI_CODING_AGENT_DIR: agent, PIG_OFFLINE: "1", PI_OFFLINE: "1" }, encoding: "utf8", timeout: 10000,
    });
    if (result.error) throw result.error;
    assert.equal(result.status, 1, result.stderr);
    assert.equal(result.signal, null);
    let selected = source;
    if (mode === "fork") {
      const files = readdirSync(sessions).filter(file => file.endsWith(".jsonl"));
      assert.equal(files.length, 1, "one requested fork");
      selected = join(sessions, files[0]);
      assert.equal(records(selected)[0].id, "chosen-fork-id");
      assert.deepEqual(names(source), []);
    }
    assert.deepEqual(names(selected), ["CLI Named Session"]);
    outcomes.push({ mode, exit: result.status, signal: result.signal, selectedID: records(selected)[0].id, names: names(selected), sourceNames: names(source) });
  }
  console.log(JSON.stringify(outcomes));
} finally {
  rmSync(root, { recursive: true, force: true });
}
