// The package names Pi's chord sources and the app's modules import, answered with the copies this process runs. It is the
// first import of host.mjs: the modules below it import Pi's chord at their own evaluation, so the hook is there before them.
import { registerHooks } from "node:module";

const tree = new URL("./pi/chord/src/", import.meta.url);
// The app's modules (core facets, host packages) import Pi's chord by its package name, as they do in Pi's own runtime: the
// names are answered with the copies this process runs, so their instances are the host's.
const CHORD = { "@earendil-works/chord": "index.ts", "@earendil-works/chord/context": "context/index.ts" };
// Pi's chord imports @earendil-works/pi-schema by its package name too. Its copy (pi/schema, the package.json beside src)
// is a package of Pi's tree, vendored as TypeScript, and Node does not strip the types of a file under node_modules, so the
// name is answered with its entry file here rather than found there.
const SCHEMA = { "@earendil-works/pi-schema": "../../schema/src/index.ts" };
// Pi's host.ts imports its `Destination` from ./destination.ts; it gets destination.mjs's, which is Pi's but for the remote types
// the Go server host serves (D605). Only that import: destination.mjs imports Pi's file itself.
const HOST = new URL("host/host.ts", tree).href;
const DESTINATION = new URL("host/destination.ts", tree).href;
const KIT_DESTINATION = new URL("./destination.mjs", import.meta.url).href;
registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = CHORD[specifier] ?? SCHEMA[specifier];
		if (file !== undefined) return { url: new URL(file, tree).href, shortCircuit: true };
		const next = nextResolve(specifier, context);
		return next.url === DESTINATION && context.parentURL === HOST ? { ...next, url: KIT_DESTINATION } : next;
	},
});
