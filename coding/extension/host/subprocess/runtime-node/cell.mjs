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
await generations.serve();
if (generations.loaded === 0) process.exitCode = 1;
