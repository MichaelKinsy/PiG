// Node's module resolution for running p2's own TypeScript (Pi's, vendored verbatim under apps/p2/web/upstream) under
// `node --test`: `@p2/app/v1` is the vendored plugin API, and each `@earendil-works/*` package is its vendored source
// (apps/p2/web/upstream-root/packages, the same files the web bundle is built from), found by the `source` condition of its
// package.json `exports`, as Pi's own `src/source.ts` resolves workspace packages.
//   node --import apps/p2/oracle/source-hooks.mjs --test <file.ts>
import { readdirSync, readFileSync } from "node:fs";
import { registerHooks } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const web = join(dirname(fileURLToPath(import.meta.url)), "..", "web");
const packages = join(web, "upstream-root", "packages");
const api = join(web, "upstream", "src", "api", "v1");

/** The vendored workspace packages by name: { directory, exports }. */
const workspace = new Map();
for (const name of readdirSync(packages)) {
	const manifest = JSON.parse(readFileSync(join(packages, name, "package.json"), "utf8"));
	workspace.set(manifest.name, { directory: join(packages, name), exports: manifest.exports ?? {} });
}

function source(specifier) {
	if (specifier === "@p2/app/v1") return join(api, "index.ts");
	if (specifier.startsWith("@p2/app/v1/")) return join(api, `${specifier.slice("@p2/app/v1/".length)}.ts`);
	const match = /^(@[^/]+\/[^/]+)(\/.*)?$/.exec(specifier);
	const entry = match && workspace.get(match[1]);
	if (!entry) return undefined;
	const target = entry.exports[match[2] ? `.${match[2]}` : "."];
	const path = typeof target === "object" ? target.source : undefined;
	return path === undefined ? undefined : join(entry.directory, path);
}

registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = source(specifier);
		return file === undefined ? nextResolve(specifier, context) : { url: pathToFileURL(file).href, shortCircuit: true };
	},
});
