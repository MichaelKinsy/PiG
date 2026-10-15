import { readFile } from "node:fs/promises";
import { adoptPiProcessIdentity } from "./process-identity.mjs";
import { Generations } from "./generations.mjs";

const manifestPath = process.argv[2];
if (!manifestPath) throw new Error("Node cell manifest is required");
const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
if (!Array.isArray(manifest) || manifest.length === 0) throw new Error("Node cell manifest is empty");
adoptPiProcessIdentity();

// pig additive (D20): the host admits factories in configured order while members share one Node process and event bus.
// The host's private stdin channel admits exactly one factory at a time. Its next admission follows that generation's register handshake and any intervening native factories. Extension traffic stays on each generation's own socket.
const generations = new Generations(manifest.length);
// The entry module does not await serve: Bun keeps a process whose entry module has a pending top-level await alive and never emits beforeExit, which the quit drain after RPC input ends depends on. A serve rejection ends the process with exit code 1, as a rejected top-level await does, whatever unhandledRejection listeners an extension added.
// served settles when the cell has served every generation: the join a top-level await of serve would be.
export const served = generations.serve().then(() => {
  if (generations.loaded === 0) process.exitCode = 1;
}).catch(error => {
  console.error(error);
  process.exit(1);
});
