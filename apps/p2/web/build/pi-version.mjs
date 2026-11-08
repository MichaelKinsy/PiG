// The version of the web bundle Pi's own `buildWebBundle` (src/hosts/bundles.ts) makes for the given plugins, run in a Pi
// checkout with its node_modules and hydrated model data (the parity reference of web/gate.sh).
//   cd <Pi checkout>/apps/p2 && node --import ./src/source.ts <this file> <plugin,plugin,...>
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const { buildWebBundle } = await import(pathToFileURL(join(process.cwd(), "src/hosts/bundles.ts")).href);
const bundle = await buildWebBundle(process.argv[2].split(","));
console.log(bundle.version);
