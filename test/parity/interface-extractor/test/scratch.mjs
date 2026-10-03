import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const live = new Set();

function removeAll() {
  for (const dir of live) fs.rmSync(dir, { recursive: true, force: true });
  live.clear();
}

// Each test file runs in its own Node process. Remove every scratch directory when that process exits, including a failing assertion, an uncaught error, and SIGINT or SIGTERM. A SIGKILL can still leave the directory, which carries the caller's pig- prefix.
process.on("exit", removeAll);
for (const [signal, code] of [["SIGINT", 130], ["SIGTERM", 143]]) {
  process.on(signal, () => process.exit(code));
}

/** Creates a scratch directory under the system temporary directory that is removed when this process exits. */
export function scratchDir(prefix) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  live.add(dir);
  return dir;
}
