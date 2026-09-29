import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { AuthStorage, ReadOnlyAuthStorage } from "../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/core/auth-storage.js";

const inputs = JSON.parse(readFileSync(new URL("./oauth-presence.json", import.meta.url), "utf8"));
const dir = mkdtempSync(join(tmpdir(), "oauth-presence-"));
try {
  const recordings = [];
  for (const [index, input] of inputs.entries()) {
    const path = join(dir, `${index}.json`);
    writeFileSync(path, JSON.stringify({ custom: input }));
    const readonly = await new ReadOnlyAuthStorage(path).read("custom");
    assert.deepEqual(readonly, input);
    const store = AuthStorage.create(path);
    const before = await store.read("custom");
    assert.deepEqual(before, input);
    const after = await store.modify("custom", async current => ({ ...current, note: "rewritten" }));
    const reopened = await AuthStorage.create(path).read("custom");
    assert.deepEqual(reopened, { ...input, note: "rewritten" });
    recordings.push({ readonly, before, after, reopened });
  }
  console.log(JSON.stringify({ recordings }));
} finally {
  rmSync(dir, { recursive: true, force: true });
}
