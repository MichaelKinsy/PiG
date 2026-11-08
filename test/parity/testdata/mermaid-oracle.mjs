// Shared by the Mermaid oracle scripts: locates the lovely-mermaid package that
// Pi's coding-agent depends on (the exact version pinned by its package.json),
// so the Go port in internal/mermaid is diffed against the source Pi renders
// with. Resolution order:
//   1. $LOVELY_MERMAID_ROOT, a lovely-mermaid package directory (one holding dist/);
//   2. <PI_PACKAGE_ROOT or .upstream/current>/node_modules/lovely-mermaid, then
//      <root>/packages/coding-agent/node_modules/lovely-mermaid. `make upstream-mirror`
//      installs the mirror's node_modules, so a mirrored Pi checkout needs no setup.
import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";

export function lovelyRoot() {
	const candidates = [];
	if (process.env.LOVELY_MERMAID_ROOT) candidates.push(process.env.LOVELY_MERMAID_ROOT);
	const piRoot = process.env.PI_PACKAGE_ROOT ?? resolve(".upstream/current");
	candidates.push(
		join(piRoot, "node_modules/lovely-mermaid"),
		join(piRoot, "packages/coding-agent/node_modules/lovely-mermaid"),
	);
	for (const dir of candidates) if (existsSync(join(dir, "dist/index.js"))) return dir;
	throw new Error(`lovely-mermaid not found; tried ${candidates.join(", ")} (run make upstream-mirror, or set LOVELY_MERMAID_ROOT)`);
}

/** Import a module of the lovely-mermaid dist, e.g. `index.js` or `diagrams/state.js`. */
export function importLovely(file) {
	return import(pathToFileURL(join(lovelyRoot(), "dist", file)).href);
}

/**
 * Import Pi's own Mermaid markdown transformer (mermaid.js of a published
 * coding-agent package, or mermaid.ts of a Pi checkout whose dependencies are
 * installed and whose pi-tui is built; Node strips the types). Resolution
 * order: $PI_MERMAID_MODULE; then under $PI_PACKAGE_ROOT (default: the pinned
 * package in extensions/sdk-ts/node_modules) `dist/.../mermaid.js` and
 * `packages/coding-agent/src/.../mermaid.ts`.
 */
export function piMermaidFile() {
	const rel = "modes/interactive/components/mermaid";
	const candidates = [];
	if (process.env.PI_MERMAID_MODULE) candidates.push(process.env.PI_MERMAID_MODULE);
	const root = process.env.PI_PACKAGE_ROOT ?? resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
	candidates.push(join(root, `dist/${rel}.js`), join(root, `packages/coding-agent/src/${rel}.ts`));
	for (const file of candidates) if (existsSync(file)) return file;
	throw new Error(`Pi's mermaid module not found; tried ${candidates.join(", ")} (set PI_MERMAID_MODULE)`);
}

export function importPiMermaid() {
	return import(pathToFileURL(piMermaidFile()).href);
}

/**
 * Import the pi-tui instance Pi's mermaid module reads terminal capabilities
 * from (resolved from the mermaid module's own location, so setCapabilities
 * changes what mermaid.ts sees through getCapabilities).
 */
export async function importPiTui() {
	const { findPackageJSON } = await import("node:module");
	const pkg = findPackageJSON("@earendil-works/pi-tui", pathToFileURL(piMermaidFile()).href);
	if (!pkg) throw new Error("@earendil-works/pi-tui not resolvable from Pi's mermaid module");
	return import(pathToFileURL(join(dirname(pkg), "dist/index.js")).href);
}
