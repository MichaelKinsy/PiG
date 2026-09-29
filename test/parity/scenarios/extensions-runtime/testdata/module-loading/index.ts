// Probes how an extension's modules load: package.json "imports" and
// "exports" maps, extensionless and emitted-extension specifiers, index
// files, JSON, CommonJS interop, import.meta and the CommonJS globals. The
// results are the description of the "module-loading" command.
import { createRequire } from "node:module";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import cjsDep from "cjs-dep";
import esmDep from "esm-dep";
import { feature } from "module-loading-probe/feature";
import data from "#data";
import { parser } from "#src/parser";
import { idx } from "./src/dir";
import json from "./src/data.json";
import { helper } from "./src/helper.js";
import legacy, { named } from "./src/legacy.cjs";
import { modern } from "./src/modern.mjs";
import { old } from "./src/old.cjs";
import { Shape, Value } from "./src/types";

const root = path.dirname(fileURLToPath(import.meta.url));
const rel = (value: unknown): unknown => {
	if (typeof value !== "string") return value === undefined ? "undefined" : value;
	return path.relative(root, value.startsWith("file:") ? fileURLToPath(value) : value).split(path.sep).join("/") || ".";
};
const attempt = (fn: () => unknown): unknown => {
	try {
		return fn();
	} catch (error) {
		return `throws ${(error as Error)?.name}`;
	}
};
const shape: Shape = { a: 1 };
const lazy = await import("./src/lazy");

const probe = {
	parser,
	data,
	helper,
	idx,
	value: Value,
	shape: shape.a,
	legacy,
	named,
	modern,
	old,
	feature,
	cjsDep,
	esmDep,
	json,
	lazy: lazy.lazy,
	dirname: attempt(() => rel(__dirname)),
	filename: attempt(() => rel(__filename)),
	metaUrl: rel(import.meta.url),
	metaDirname: rel(import.meta.dirname),
	metaFilename: rel(import.meta.filename),
	metaResolve: attempt(() => rel(import.meta.resolve("#src/parser.ts"))),
	require: typeof require,
	requireCjs: attempt(() => require("./src/legacy.cjs").named),
	requireResolve: attempt(() => rel(require.resolve("cjs-dep"))),
	requireConditional: attempt(() => require("esm-dep")),
	module: typeof module,
	exports: typeof exports,
	createRequire: createRequire(import.meta.url)("./src/legacy.cjs").named,
};

export default function (pi: any) {
	pi.registerCommand("module-loading", {
		description: JSON.stringify(probe),
		handler: async () => {},
	});
}
