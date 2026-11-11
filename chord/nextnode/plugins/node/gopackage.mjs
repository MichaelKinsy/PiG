// Host packages the Go app offers (GoPackage, docs/plan/chord-plugins-node-evaluator.md 10.4): an ES module per specifier whose
// exports are asynchronous functions that `call` a Go function (callbacks.mjs) and JSON values. The module is answered to the
// specifier by `module.registerHooks`, so a plugin's `require` (through the runtime's host packages) and an app module's
// `import` of the specifier get the same instance.
import { registerHooks } from "node:module";

const callbacks = new URL("./callbacks.mjs", import.meta.url).href;
/** The generated source of each module, by URL. */
const sources = new Map();
/** The URL of each package, by specifier. */
const urls = new Map();

registerHooks({
	resolve(specifier, context, nextResolve) {
		const url = sources.has(specifier) ? specifier : urls.get(specifier);
		return url === undefined ? nextResolve(specifier, context) : { url, shortCircuit: true };
	},
	load(url, context, nextLoad) {
		const source = sources.get(url);
		return source === undefined ? nextLoad(url, context) : { format: "module", source, shortCircuit: true };
	},
});

/**
 * The module source of a package: each export is `{ name, call }` (an asynchronous function `(args, context?)` that calls the
 * Go function named `call` with the JSON `args`, and resolves with its JSON result; `context` is the request's chord Context or
 * AbortSignal, whose abort cancels the call) or `{ name, value }` (the JSON value, parsed once).
 */
function moduleSource(pkg) {
	const lines = [`import { call, signalOf } from ${JSON.stringify(callbacks)};`];
	pkg.exports.forEach((each, index) => {
		const binding = `e${index}`;
		if (each.call !== undefined) {
			lines.push(`const ${binding} = async (args, context) => call(${JSON.stringify(each.call)}, args, signalOf(context));`);
		} else {
			lines.push(`const ${binding} = JSON.parse(${JSON.stringify(JSON.stringify(each.value))});`);
		}
		lines.push(`export { ${binding} as ${JSON.stringify(each.name)} };`);
	});
	return lines.join("\n");
}

/** Imports the module of `pkg` (`{ specifier, exports }`, as the Go side describes it) and returns its namespace. */
export function importGoPackage(pkg) {
	const url = `pig-go-package:${urls.size + 1}`;
	urls.set(pkg.specifier, url);
	sources.set(url, moduleSource(pkg));
	return import(url);
}
