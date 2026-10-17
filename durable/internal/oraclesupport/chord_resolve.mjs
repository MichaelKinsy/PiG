// chordSourceURL resolves an @earendil-works/chord specifier to its source file in the mirrored Pi packages directory through
// chord's package.json exports map (`./sqlite/node` is src/sqlite/node.ts, `./context` is src/context/index.ts), as the
// monorepo's own resolution does; any other specifier returns undefined. The oracles load Pi's sources with types stripped.
import fs from "node:fs";
import { pathToFileURL } from "node:url";

const exportMaps = new Map();

export function chordSourceURL(packages, specifier) {
	if (specifier !== "@earendil-works/chord" && !specifier.startsWith("@earendil-works/chord/")) return undefined;
	let exports = exportMaps.get(packages);
	if (!exports) {
		exports = JSON.parse(fs.readFileSync(`${packages}chord/package.json`, "utf8")).exports;
		exportMaps.set(packages, exports);
	}
	const subpath = `.${specifier.slice("@earendil-works/chord".length)}`;
	const target = exports[subpath];
	const source = typeof target === "string" ? target : target?.source;
	if (!source) throw new Error(`@earendil-works/chord exports no source for ${subpath}`);
	return pathToFileURL(`${packages}chord/${source.replace(/^\.\//, "")}`).href;
}
