// The Pi side of the contract fingerprint oracle: loads p2's own TypeScript contract modules (apps/p2/web/upstream, Pi's
// files verbatim) on Pi's Chord (the copy vendored under chord/nextnode/plugins/node/pi) and prints, for every contract
// a module exports, its name, version and fingerprint as JSON on stdout.
//   node fingerprints.mjs <repo root> <module path relative to apps/p2/web/upstream>...
import { readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const [root, ...modules] = process.argv.slice(2);
const chord = join(root, "chord/nextnode/plugins/node/pi/chord/src/");
const answers = {
	"@earendil-works/chord": join(root, "apps/p2/oracle/recording-chord.mjs"),
	"@earendil-works/chord/context": join(chord, "context/index.ts"),
	"@earendil-works/pi-schema": join(root, "chord/nextnode/plugins/node/pi/schema/src/index.ts"),
	"@p2/app/v1": join(root, "apps/p2/web/upstream/src/api/v1/index.ts"),
};

// A module whose imports this oracle cannot answer (the worker's Durable, environments and Node packages, the files of its own
// directory that are not vendored) is loaded all the same: the contract a module declares is built from Chord and the schema
// package alone, so each import nothing here answers is a stub that takes any use (a call, a construction, a property) and
// gives a stub back. The stub exports the names the importing module imports from it.
const stubbed = new Map();
function importedNames(parent, specifier) {
	const source = readFileSync(fileURLToPath(parent), "utf8");
	const names = new Set();
	const quoted = specifier.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
	for (const match of source.matchAll(new RegExp(`(?:import|export)\\s+(type\\s+)?([^;]*?)\\s+from\\s+["']${quoted}["']`, "gs"))) {
		if (match[1]) continue;
		const clause = match[2];
		const braces = /\{([^}]*)\}/s.exec(clause);
		const outside = clause.replace(/\{[^}]*\}/s, "").replace(/,/g, " ").trim();
		if (outside && !outside.startsWith("*")) names.add("default");
		for (const part of braces?.[1].split(",") ?? []) {
			const name = part.trim();
			if (!name || name.startsWith("type ")) continue;
			names.add(name.split(/\s+as\s+/)[0].trim());
		}
	}
	return [...names];
}
const STUB = `const stub = new Proxy(function () {}, {
	get: (_, key) => (key === "then" || typeof key === "symbol" ? undefined : stub),
	apply: () => stub,
	construct: () => stub,
});
export default stub;
`;
registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = answers[specifier];
		if (file !== undefined) return { url: pathToFileURL(file).href, shortCircuit: true };
		try {
			return nextResolve(specifier, context);
		} catch (error) {
			if (!context.parentURL || !["ERR_MODULE_NOT_FOUND", "ERR_PACKAGE_PATH_NOT_EXPORTED", "ERR_UNSUPPORTED_DIR_IMPORT"].includes(error?.code)) throw error;
			const names = importedNames(context.parentURL, specifier).filter((name) => name !== "default");
			const url = `p2-stub:${encodeURIComponent(specifier)}?${names.join(",")}`;
			stubbed.set(url, names);
			return { url, shortCircuit: true };
		}
	},
	load(url, context, nextLoad) {
		if (!url.startsWith("p2-stub:")) return nextLoad(url, context);
		const names = stubbed.get(url) ?? [];
		return { format: "module", shortCircuit: true, source: `${STUB}${names.map((name) => `export const ${name} = stub;`).join("\n")}` };
	},
});

// The recording copy of Chord's entry names the real one by a placeholder; it is loaded with the placeholder answered.
answers.ORACLE_CHORD_INDEX = join(chord, "index.ts");

const out = {};
const recording = await import(pathToFileURL(answers["@earendil-works/chord"]).href);
for (const module of modules) {
	recording.declared.length = 0;
	const exported = await import(pathToFileURL(join(root, "apps/p2/web/upstream", module)).href);
	// Every contract the module declared, exported or not, by its name.
	for (const value of recording.declared)
		out[`${module}#declared:${value.name}`] = { name: value.name, version: value.version, fingerprint: value.fingerprint };
	for (const [name, value] of Object.entries(exported)) {
		if (value && typeof value === "object" && typeof value.fingerprint === "string" && typeof value.name === "string")
			out[`${module}#${name}`] = { name: value.name, version: value.version, fingerprint: value.fingerprint };
	}
}
process.stdout.write(`${JSON.stringify(out)}\n`);
