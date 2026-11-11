// Resolution for Pi's own Node test files (`node --import this --test <file>`): the vendored web modules import the packages
// Pi's repository root installs (the schema package, Chord), which this repository does not install under apps/p2/web: they
// are answered with the copies of them PiG vendors for its plugin runtime (the same ones the contract oracle loads,
// fingerprints.mjs), so the tests run on the modules they import and nothing is stubbed.
import { registerHooks } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "../../..");
const pi = join(root, "chord/nextnode/plugins/node/pi");
const answers = {
	"@earendil-works/chord": join(pi, "chord/src/index.ts"),
	"@earendil-works/chord/context": join(pi, "chord/src/context/index.ts"),
	"@earendil-works/pi-schema": join(pi, "schema/src/index.ts"),
	"@p2/app/v1": join(root, "apps/p2/web/upstream/src/api/v1/index.ts"),
};

registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = answers[specifier];
		if (file !== undefined) return { url: pathToFileURL(file).href, shortCircuit: true };
		return nextResolve(specifier, context);
	},
});
