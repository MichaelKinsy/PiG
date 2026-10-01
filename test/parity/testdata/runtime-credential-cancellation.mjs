import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

const operations = ["read", "list", "delete"];
if (process.argv[2] === "pig") {
  const output = execFileSync("go", ["test", "./ai", "-run", "^TestRuntimeCredentialsPreservesCancellationCause$", "-count=1", "-v"], { encoding: "utf8" });
  const records = output.split("\n").filter(line => line.startsWith("RUNTIME_CREDENTIAL_CANCEL "));
  assert.equal(records.length, operations.length);
  for (const record of records) console.log(record);
} else {
  assert.equal(process.argv[2], "pi");
  const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
  assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "0.99.2");
  const { RuntimeCredentials } = await import(pathToFileURL(join(root, "dist/core/runtime-credentials.js")));
  for (const operation of operations) {
    let calls = 0;
    const store = { read: async () => { calls++; }, list: async () => { calls++; return []; }, delete: async () => { calls++; } };
    const credentials = new RuntimeCredentials(store);
    credentials.setRuntimeApiKey("provider", "runtime-key");
    const controller = new AbortController();
    const reason = new Error(`caller cancelled ${operation}`);
    controller.abort(reason);
    let failure;
    try {
      if (operation === "list") await credentials.list({ signal: controller.signal });
      else await credentials[operation]("provider", { signal: controller.signal });
    } catch (error) { failure = error; }
    assert.equal(failure, reason);
    assert.equal(calls, operation === "list" ? 1 : 0);
    assert.equal(credentials.hasRuntimeApiKey("provider"), true);
    console.log("RUNTIME_CREDENTIAL_CANCEL " + JSON.stringify([operation, failure.message, calls]));
  }
}
