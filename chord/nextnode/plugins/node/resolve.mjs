// The package names Pi's chord sources and the app's modules import, answered with the copies this process runs. It is the
// first import of host.mjs: the modules below it import Pi's chord at their own evaluation, so the hook is there before them.
import { registerHooks } from "node:module";

const tree = new URL("./pi/chord/src/", import.meta.url);
// The app's modules (core facets, host packages) import Pi's chord by its package name, as they do in Pi's own runtime: the
// names are answered with the copies this process runs, so their instances are the host's.
const CHORD = { "@earendil-works/chord/next": "next/index.ts", "@earendil-works/chord/context": "context/index.ts" };
// Pi's chord imports @earendil-works/pi-schema by its package name too. Its copy (pi/schema, the package.json beside src)
// is a package of Pi's tree, vendored as TypeScript, and Node does not strip the types of a file under node_modules, so the
// name is answered with its entry file here rather than found there.
const SCHEMA = { "@earendil-works/pi-schema": "../../schema/src/index.ts" };
registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = CHORD[specifier] ?? SCHEMA[specifier];
		return file === undefined ? nextResolve(specifier, context) : { url: new URL(file, tree).href, shortCircuit: true };
	},
});
