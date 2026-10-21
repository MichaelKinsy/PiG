// pig additive (D19): Pi's withFileMutationQueue (core/tools/file-mutation-queue.ts) is one module-level queue in one process, shared by the built-in edit and write
// tools and every extension. Here the built-in tools run in PiG's host, so the queue is the host's: the vendored edit and write tools and every extension that imports
// withFileMutationQueue from Pi load this module in place of Pi's (automation/gen/vendor-pi-dist.sh lists the override), which asks the host's queue for the file's
// turn (fileMutation.enter, answered once the file's earlier mutations completed) and gives it back (fileMutation.exit). The path is resolved here, against this
// process's working directory, as Pi's `resolve(filePath)` does; the host takes its real path as the key. A process with no extension runtime (a forked child or
// worker an extension starts) has no connection to the host and keeps Pi's own per-process queue, which the vendored copy of Pi's module still provides.
import { resolve } from "node:path";
import { currentRuntime } from "../state.mjs";
import { withFileMutationQueue as processQueue } from "./pi-dist/pi-coding-agent/core/tools/file-mutation-queue.js";

/**
 * Serialize file mutation operations targeting the same file.
 * Operations for different files still run in parallel.
 */
export async function withFileMutationQueue(filePath, fn) {
  const runtime = currentRuntime();
  if (!runtime) return processQueue(filePath, fn);
  const entered = await runtime.fileMutation("fileMutation.enter", { path: resolve(filePath) });
  try {
    return await fn();
  } finally {
    // The host also releases the turn when the connection closes, so a failed exit leaves nothing held.
    await runtime.fileMutation("fileMutation.exit", { ticket: entered.ticket }).catch(() => {});
  }
}
