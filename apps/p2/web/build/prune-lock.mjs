// Writes package.json and package-lock.json of this directory: the part of Pi's root package-lock.json that the web bundle
// resolves in node_modules (the direct dependencies below and everything they depend on), with Pi's exact versions and hashes.
//   node prune-lock.mjs <Pi checkout's package-lock.json>
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const direct = ["@earendil-works/design-tokens", "@earendil-works/elanor-fonts", "@earendil-works/elanor-icons-react", "@earendil-works/elanor-react", "articulated", "lowlight", "marked", "partial-json", "react", "react-dom"];
const lock = JSON.parse(readFileSync(process.argv[2], "utf8"));
// apps/p2's own node_modules entries (marked) shadow the hoisted ones for files of apps/p2, so they take the root's place.
for (const key of Object.keys(lock.packages))
	if (key.startsWith("apps/p2/node_modules/") && direct.includes(key.slice("apps/p2/node_modules/".length)))
		lock.packages[key.slice("apps/p2/".length)] = lock.packages[key];
// Node's lookup: node_modules of the package itself, then of each ancestor package, then the root's.
const resolveFrom = (from, name) => {
	for (let at = from; ; ) {
		const key = at === "" ? `node_modules/${name}` : `${at}/node_modules/${name}`;
		if (lock.packages[key]) return key;
		if (at === "") return undefined;
		const parent = at.lastIndexOf("node_modules/");
		at = parent <= 0 ? "" : at.slice(0, parent - 1);
	}
};
const keep = new Set();
const walk = (key) => {
	if (keep.has(key)) return;
	keep.add(key);
	const entry = lock.packages[key];
	for (const kind of ["dependencies", "optionalDependencies", "peerDependencies"]) {
		for (const name of Object.keys(entry[kind] ?? {})) {
			if (kind === "peerDependencies" && entry.peerDependenciesMeta?.[name]?.optional) continue;
			const found = resolveFrom(key, name);
			if (found) walk(found);
			else if (kind !== "optionalDependencies") throw new Error(`${key}: ${name} not in the lock`);
		}
	}
};
const rootDependencies = Object.fromEntries(direct.map((name) => [name, lock.packages[`node_modules/${name}`].version]));
for (const name of direct) walk(`node_modules/${name}`);
const packages = { "": { name: "p2-web-build", version: "0.0.0", private: true, dependencies: rootDependencies } };
for (const key of [...keep].sort()) packages[key] = lock.packages[key];
writeFileSync(join(here, "package.json"), `${JSON.stringify({ name: "p2-web-build", version: "0.0.0", private: true, type: "module", dependencies: { ...rootDependencies, esbuild: lock.packages["node_modules/esbuild"].version } }, null, "\t")}\n`);
packages[""].dependencies = { ...rootDependencies, esbuild: lock.packages["node_modules/esbuild"].version };
const esbuildKeys = Object.keys(lock.packages).filter((key) => key.startsWith("node_modules/@esbuild/") || key === "node_modules/esbuild");
for (const key of esbuildKeys) packages[key] = lock.packages[key];
writeFileSync(join(here, "package-lock.json"), `${JSON.stringify({ name: "p2-web-build", version: "0.0.0", lockfileVersion: lock.lockfileVersion, requires: true, packages }, null, "\t")}\n`);
console.log(Object.keys(packages).length, "packages");
