// The Pi side of the contract fingerprint oracle: loads p2's own TypeScript contract modules (apps/p2/web/upstream, Pi's
// files verbatim) on Pi's Chord (the copy vendored under chord/nextnode/plugins/node/pi) and prints, for every contract
// a module exports, its name, version and fingerprint as JSON on stdout.
//   node fingerprints.mjs <repo root> <module path relative to apps/p2/web/upstream>...
import { registerHooks } from "node:module";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const [root, ...modules] = process.argv.slice(2);
const chord = join(root, "chord/nextnode/plugins/node/pi/chord/src/");
const answers = {
	"@earendil-works/chord": join(chord, "index.ts"),
	"@earendil-works/chord/context": join(chord, "context/index.ts"),
	"@earendil-works/pi-schema": join(root, "chord/nextnode/plugins/node/pi/schema/src/index.ts"),
	"@p2/app/v1": join(root, "apps/p2/web/upstream/src/api/v1/index.ts"),
};
registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = answers[specifier];
		return file === undefined ? nextResolve(specifier, context) : { url: pathToFileURL(file).href, shortCircuit: true };
	},
});

const out = {};
for (const module of modules) {
	const exported = await import(pathToFileURL(join(root, "apps/p2/web/upstream", module)).href);
	for (const [name, value] of Object.entries(exported)) {
		if (value && typeof value === "object" && typeof value.fingerprint === "string" && typeof value.name === "string")
			out[`${module}#${name}`] = { name: value.name, version: value.version, fingerprint: value.fingerprint };
	}
}
process.stdout.write(`${JSON.stringify(out)}\n`);
