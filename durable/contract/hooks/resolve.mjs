// The ESM hooks (CONTRACT section 6, E00-E31). `resolve` does four things, all after the default resolution:
//   1. maps a reference package whose export map has no `source` entry (pi-ai's wildcards) from dist/x.js to src/x.ts;
//   2. swaps the storage constructors for capsql-backed ones (impls/storage.mjs);
//   3. wraps the model registry and uuidv7() so every model call and every generated id reaches the tape;
//   4. aliases the reference's public entry point to the candidate's JS API when a candidate is selected.
// `load` generates each wrapper: `export *` of the real module (imported with the `?real` query, which `resolve` leaves
// alone) plus the overrides named in `swaps`, taken from the wrapper module's same-named export.
import { existsSync, realpathSync } from "node:fs"
import { fileURLToPath, pathToFileURL } from "node:url"

let cfg
export function initialize(data) { cfg = data }

// [pattern of the real module's URL, wrapper module, names the wrapper overrides, whether the real module is re-exported]
const swaps = [
	[/\/packages\/durable\/src\/storage\/memory\.ts$/, "impls/storage.mjs", ["MemoryStorage"], false],
	[/\/packages\/durable\/src\/storage\/jsonl\/node\.ts$/, "impls/storage.mjs", ["openNodeJsonlStorage"], true],
	[/\/packages\/durable\/src\/storage\/sqlite\/node\.ts$/, "impls/storage.mjs", ["openNodeSqliteStorage"], true],
	[/\/packages\/ai\/src\/models\.ts$/, "impls/models.mjs", ["createModels"], true],
	[/\/packages\/ai\/src\/utils\/uuid\.ts$/, "impls/uuid.mjs", ["uuidv7"], true],
]

// dist/x.js -> src/x.ts, and a workspace link under node_modules to the package's real directory (Node refuses to strip
// types below node_modules, and the real directory is where the reference's own sources import each other from).
const distToSrc = url => {
	const src = url.replace(/\/dist\/(.*)\.js$/, "/src/$1.ts")
	if (!/\/node_modules\/@earendil-works\//.test(src)) return src
	try { return pathToFileURL(realpathSync(fileURLToPath(src))).href } catch { return src }
}
const isWorkspaceLink = url => /\/node_modules\/@earendil-works\//.test(url)
const isBare = specifier => !/^(\.{0,2}\/|[a-z][a-z0-9+.-]*:)/i.test(specifier)
const WRAP = "contract-wrap:"
const API_TRACE = "contract-api-trace:"

export async function resolve(specifier, context, nextResolve) {
	const real = specifier.includes("?real")
	// CONTRACT_STORAGE_PROFILE=node: the reference's Node SQLite adapter runs unchanged over a capsql DatabaseSync (impls/node-sqlite-capture.mjs).
	if (cfg.storageProfile === "node" && specifier === "node:sqlite" && /\/storage\/sqlite\/node\.ts/.test(context.parentURL ?? "")) return { url: new URL("impls/node-sqlite-capture.mjs", cfg.contract).href, shortCircuit: true }
	// The wrappers, the tools and a candidate live outside the checkout: their package imports resolve from the checkout.
	if (isBare(specifier) && context.parentURL && !context.parentURL.startsWith(cfg.piRoot)) context = { ...context, parentURL: `${cfg.piRoot}packages/durable/src/index.ts` }
	let result
	try {
		result = await nextResolve(specifier, { ...context, conditions: [...context.conditions, "source"] })
	} catch (error) {
		const url = error?.url ?? ""
		if (error?.code !== "ERR_MODULE_NOT_FOUND" || !url.includes("/dist/") || (url.includes("/node_modules/") && !isWorkspaceLink(url))) throw error
		result = await nextResolve(distToSrc(url), context)
	}
	if (result.url.includes("/dist/") && result.url.startsWith(cfg.piRoot) && (!result.url.includes("/node_modules/") || isWorkspaceLink(result.url))) result = { ...result, url: distToSrc(result.url) }
	if (real || !result.url.startsWith(cfg.piRoot)) return result
	// A candidate is a directory laid out like packages/durable/src: every module it has replaces the reference's.
	const src = `${cfg.piRoot}packages/durable/src/`
	if (cfg.candidate && result.url.startsWith(src)) {
		const mine = new URL(result.url.slice(src.length), cfg.candidate)
		if (existsSync(fileURLToPath(mine))) result = { url: mine.href, shortCircuit: true }
	}
	// CONTRACT_API_TRACE: the public entry point (the reference's or the candidate's index.ts) exports a Harness whose API calls are traced.
	if (cfg.apiTrace && /\/index\.ts$/.test(result.url) && (result.url === `${src}index.ts` || (cfg.candidate && result.url === new URL("index.ts", cfg.candidate).href))) return { url: `${API_TRACE}?target=${encodeURIComponent(result.url)}`, shortCircuit: true }
	if (result.shortCircuit) return result
	const index = swaps.findIndex(([pattern]) => pattern.test(result.url))
	if (index >= 0) return { url: `${WRAP}${index}?target=${encodeURIComponent(result.url)}`, shortCircuit: true }
	return result
}

export async function load(url, context, nextLoad) {
	if (url.startsWith(API_TRACE)) {
		const target = new URL(url).searchParams.get("target")
		const tracer = new URL("lib/apitrace.mjs", cfg.contract).href
		return { format: "module", shortCircuit: true, source: [`import { traceHarness } from ${JSON.stringify(tracer)}`, `export * from ${JSON.stringify(`${target}?real`)}`, `import * as real from ${JSON.stringify(`${target}?real`)}`, "export const Harness = traceHarness(real.Harness)"].join("\n") }
	}
	if (!url.startsWith(WRAP)) return nextLoad(url, context)
	const index = Number(url.slice(WRAP.length).split("?")[0])
	const target = new URL(url).searchParams.get("target")
	const [, wrapper, names, reexport] = swaps[index]
	const w = new URL(wrapper, cfg.contract).href
	const lines = [`import * as w from ${JSON.stringify(w)}`]
	if (reexport) lines.push(`export * from ${JSON.stringify(`${target}?real`)}`, `import * as real from ${JSON.stringify(`${target}?real`)}`)
	for (const name of names) lines.push(`export const ${name} = w.${name}(${reexport ? `real.${name}` : ""})`)
	return { format: "module", shortCircuit: true, source: lines.join("\n") }
}
