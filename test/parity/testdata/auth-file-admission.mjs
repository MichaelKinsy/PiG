import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const operations = ["modify", "delete"];
if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./ai", "-run", "^TestFileAuthStorageQueuedMutationCancellation$", "-count=1", "-v"], { encoding: "utf8" });
  const records = output.split("\n").filter(line => line.startsWith("FILE_AUTH_ADMISSION "));
  assert.equal(records.length, operations.length);
  for (const record of records) console.log(record);
} else {
  assert.equal(process.argv[2], "pi");
  const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
  assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.0");
  const { AuthStorage } = await import(pathToFileURL(join(root, "dist/core/auth-storage.js")));
  for (const operation of operations) {
    const dir = mkdtempSync(join(process.argv[3], "file-auth-admission-"));
    let release, first;
    try {
      const path = join(dir, "auth.json");
      const store = AuthStorage.create(path);
      await store.modify("queued", async () => ({ type: "api_key", key: "keep" }));
      let started;
      const active = new Promise(resolve => { started = resolve; });
      const finish = new Promise(resolve => { release = resolve; });
      first = store.modify("first", async () => { started(); await finish; return { type: "api_key", key: "committed" }; });
      await active;
      const controller = new AbortController();
      let ran = false, completed = false;
      const pending = operation === "modify"
        ? store.modify("queued", async () => { ran = true; return { type: "api_key", key: "wrong" }; }, { signal: controller.signal })
        : store.delete("queued", { signal: controller.signal });
      const reason = new Error("queued file operation cancelled");
      controller.abort(reason);
      await assert.rejects(pending, error => error === reason);
      completed = true;
      assert.equal(ran, false);
      release();
      await first;
      const queuedKey = (await store.read("queued")).key;
      const firstKey = (await store.read("first")).key;
      assert.equal(queuedKey, "keep");
      assert.equal(firstKey, "committed");
      assert.equal(existsSync(path + ".lock"), false);
      console.log("FILE_AUTH_ADMISSION " + JSON.stringify([operation, completed, ran, queuedKey, firstKey]));
    } finally {
      release?.();
      await first?.catch(() => {});
      rmSync(dir, { recursive: true, force: true });
    }
  }
}
