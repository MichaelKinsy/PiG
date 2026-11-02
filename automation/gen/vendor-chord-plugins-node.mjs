// Copy Pi's own chord sources for the plugins component's Node subprocess (chord/nextnode/plugins/node/pi) and write
// the manifest that records the Pi commit and the hash of every copied file.
//
//   node vendor-chord-plugins-node.mjs <pi tree root> <pi commit> <pi-telemetry package dir> <out node dir> <pin commit>
//
// The manifest records the pin the copy was made against; when the copied commit is not the pin's, it names that commit as
// `catchup` (the commit the manifest may differ from the pin by: the Go test accepts exactly it, while the pin stays put).
//
// packages/chord/src is copied byte for byte, keeping its layout, for the import closure of the entries the subprocess
// loads (Node strips the types of the copies; nothing is transpiled). The closure is found on the type-stripped source,
// so `import type` edges, which Node never loads, do not pull files in. @earendil-works/pi-schema and
// @earendil-works/pi-telemetry are Pi's two runtime dependencies of that closure. pi-schema is a package of the same Pi tree
// (packages/schema, never published): its src closure is copied byte for byte to pi/schema, with its package.json and
// LICENSES, and node/host.mjs resolves the package name to it, because Node refuses to strip the types of a file under
// node_modules. pi-telemetry's dist modules are copied as a package in node_modules.
import { createHash } from "node:crypto";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import { dirname, join, relative, resolve, sep } from "node:path";

const [piTree, commit, telemetryDir, outDir, pin] = process.argv.slice(2);
if (pin === undefined) throw new Error("usage: vendor-chord-plugins-node.mjs <pi tree> <commit> <pi-telemetry> <out> <pin>");

const ENTRIES = [
	"next/index.ts",
	"next-node/plugins/evaluate.ts",
	"next-node/plugins/stack.ts",
	"next-node/plugins/runtime.ts",
	"next-node/compression.ts",
	"context/index.ts",
	// The Node host's store (node/store.mjs): the SQLite backend over node:sqlite. database.ts is the interface node.ts
	// imports as a type only, so Node's stripping leaves it out of the closure; it is copied as Pi's own file beside it.
	"sqlite/node.ts",
	"sqlite/database.ts",
];
/** The bare specifiers the closure may import: Pi's runtime dependencies that are copied below. */
const PACKAGES = new Set(["@earendil-works/pi-schema", "@earendil-works/pi-telemetry"]);

const src = join(piTree, "packages/chord/src");
const schemaDir = join(piTree, "packages/schema");
const schemaSrc = join(schemaDir, "src");
const closure = new Set();
const schemaClosure = new Set();
/** Adds file and the relative files it imports to `into`, which is `closure` for chord's sources and `schemaClosure` for pi-schema's. */
function visit(file, into = closure) {
	if (into.has(file)) return;
	into.add(file);
	const js = stripTypeScriptTypes(readFileSync(file, "utf8"));
	for (const match of js.matchAll(/(?:\bfrom\s*|\bimport\s*\(?\s*)["']([^"'\n]+)["']/g)) {
		const specifier = match[1];
		if (specifier.startsWith(".")) {
			const target = resolve(dirname(file), specifier);
			if (!existsSync(target) || !statSync(target).isFile()) throw new Error(`${relative(src, file)}: ${specifier} does not exist`);
			visit(target, into);
		} else if (/^(@earendil-works\/|typebox)/.test(specifier) && !PACKAGES.has(specifier)) {
			throw new Error(`${relative(src, file)}: ${specifier} is a package this copy does not carry`);
		} else if (into === schemaClosure && specifier !== "@earendil-works/pi-schema" && !specifier.startsWith("node:")) {
			throw new Error(`${relative(schemaSrc, file)}: ${specifier} is a package pi-schema's copy does not carry`);
		}
	}
}
for (const entry of ENTRIES) visit(join(src, entry));
// The package chord imports by name: its root entry (chord's closure imports nothing of its subpaths, which the manifest's
// closure would otherwise name).
visit(join(schemaSrc, "index.ts"), schemaClosure);

const pi = join(outDir, "pi");
rmSync(pi, { recursive: true, force: true });
const files = [];
function copy(from, to) {
	mkdirSync(dirname(to), { recursive: true });
	cpSync(from, to);
	files.push(relative(outDir, to).split(sep).join("/"));
}
for (const file of [...closure].sort()) copy(file, join(pi, "chord/src", relative(src, file)));

function walk(dir) {
	return readdirSync(dir, { withFileTypes: true }).flatMap((entry) =>
		entry.isDirectory() ? walk(join(dir, entry.name)) : [join(dir, entry.name)],
	);
}
for (const file of [...schemaClosure].sort()) copy(file, join(pi, "schema/src", relative(schemaSrc, file)));
copy(join(schemaDir, "package.json"), join(pi, "schema/package.json"));
for (const file of walk(join(schemaDir, "LICENSES")).sort()) copy(file, join(pi, "schema/LICENSES", relative(join(schemaDir, "LICENSES"), file)));
copy(join(telemetryDir, "package.json"), join(pi, "node_modules/@earendil-works/pi-telemetry/package.json"));
for (const file of walk(join(telemetryDir, "dist")).sort()) {
	const name = relative(telemetryDir, file).split(sep).join("/");
	if (name.endsWith(".js") && !name.startsWith("dist/testing/")) copy(file, join(pi, "node_modules/@earendil-works/pi-telemetry", name));
}

const manifest = {
	commit,
	pin,
	...(commit === pin ? {} : { catchup: commit }),
	piSchema: JSON.parse(readFileSync(join(schemaDir, "package.json"), "utf8")).version,
	piTelemetry: JSON.parse(readFileSync(join(telemetryDir, "package.json"), "utf8")).version,
	files: files.sort().map((path) => ({ path, sha256: createHash("sha256").update(readFileSync(join(outDir, path))).digest("hex") })),
};
writeFileSync(join(outDir, "manifest.json"), `${JSON.stringify(manifest, null, "\t")}\n`);
console.log(`vendored ${closure.size} chord sources of Pi ${commit}, ${schemaClosure.size} pi-schema sources ${manifest.piSchema}, pi-telemetry ${manifest.piTelemetry} into ${pi}`);
