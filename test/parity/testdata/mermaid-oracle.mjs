// Shared by the Mermaid oracle scripts: locates the lovely-mermaid package that
// Pi's coding-agent depends on (the exact version pinned by its package.json),
// so the Go port in internal/mermaid is diffed against the source Pi renders
// with. Resolution order:
//   1. $LOVELY_MERMAID_ROOT, a lovely-mermaid package directory (one holding dist/);
//   2. <PI_PACKAGE_ROOT or .upstream/current>/node_modules/lovely-mermaid, then
//      <root>/packages/coding-agent/node_modules/lovely-mermaid. `make upstream-mirror`
//      installs the mirror's node_modules, so a mirrored Pi checkout needs no setup.
import { existsSync } from "node:fs";
import { join, resolve } from "node:path";
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
