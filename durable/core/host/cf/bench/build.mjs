// Builds the two programs the durable-core benchmark profile (bench/k8s) runs on Node, each one self-contained file:
//
//   node durable/core/host/cf/bench/build.mjs --pi <Pi checkout at durable/contract/PIN main> --out <dir>
//
//   <dir>/bench-pi.mjs   durable/interop/bench.mjs on pi-durable, pi-ai and chord from the Pi checkout's sources: the
//                        reference the core's conformance gate captures its corpus from (main, not an npm release).
//   <dir>/bench-pig.mjs  the same program on the PiG Durable core through this host's pi-durable-compatible facade
//                        (src/facade, node:sqlite), with pi-ai and chord from the same Pi sources.
//
// Both resolve @earendil-works packages to the checkout's TypeScript sources through their export maps (as durable-bench
// builds its pi-head target), bundle every other dependency, and leave node: built-ins external. bench-pig.mjs runs the core
// module DCORE_WASM when set, else core-tinygo.wasm from the dist/ directory beside <dir> (the benchmark bundle's layout). Each file names
// its target and implementation in bench.mjs's lines (BENCH_TARGET, BENCH_DURABLE_VERSION).
import { build } from "esbuild"
import { execFileSync } from "node:child_process"
import { createHash } from "node:crypto"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { parseArgs } from "node:util"

const { values } = parseArgs({ options: { pi: { type: "string" }, out: { type: "string" } } })
if (!values.pi || !values.out) throw new Error("usage: build.mjs --pi <Pi checkout> --out <dir>")
const pi = resolve(values.pi), out = resolve(values.out)
const here = dirname(fileURLToPath(import.meta.url))
const host = resolve(here, "..")
const repo = resolve(host, "../../../..")
const benchMjs = join(repo, "durable/interop/bench.mjs")
const facade = join(host, "src/facade/index.ts")
const pin = /^main\s+([0-9a-f]{40})$/m.exec(readFileSync(join(repo, "durable/contract/PIN"), "utf8"))?.[1]
if (!pin) throw new Error("durable/contract/PIN names no main commit")

const dirs = { "pi-durable": "durable", "pi-ai": "ai", chord: "chord" }
/** @earendil-works/<pkg>[/sub] -> the Pi checkout's source file, through the package's export map. */
function piSource(path) {
	const [, pkg, sub = ""] = /^@earendil-works\/([^/]+)(\/.*)?$/.exec(path)
	const root = join(pi, "packages", dirs[pkg] ?? pkg)
	const exports = JSON.parse(readFileSync(join(root, "package.json"), "utf8")).exports
	const key = "." + sub
	let entry = exports[key], star = ""
	if (entry === undefined) {
		const pattern = Object.keys(exports).find(k => k.endsWith("*") && key.startsWith(k.slice(0, -1)))
		if (!pattern) throw new Error(`no export ${key} in @earendil-works/${pkg}`)
		star = key.slice(pattern.length - 1)
		entry = exports[pattern]
	}
	const target = typeof entry === "string" ? entry : (entry.source ?? entry.import)
	return join(root, target.replace("*", star).replace(/^\.\/dist\/(.*)\.js$/, "./src/$1.ts"))
}

const resolver = facadeFor => ({
	name: "pi-source",
	setup(b) {
		b.onResolve({ filter: /^@earendil-works\// }, args => {
			// bench.mjs's pi-durable entry points are the facade in the PiG program; the facade's own imports stay Pi's.
			const fromBench = args.importer === benchMjs
			if (facadeFor && fromBench && /^@earendil-works\/pi-durable(\/storage\/sqlite\/node)?$/.test(args.path)) return { path: facade }
			return { path: piSource(args.path) }
		})
		// Pi's own sources import their npm dependencies from the checkout's node_modules.
		b.onResolve({ filter: /^[^./]/ }, async args => {
			if (args.path.startsWith("node:") || args.path.startsWith("@earendil-works/") || args.pluginData === "retry") return
			if (!args.importer.startsWith(pi)) return
			return b.resolve(args.path, { kind: args.kind, resolveDir: pi, pluginData: "retry" })
		})
	},
})

const scratch = mkdtempSync(join(tmpdir(), "bench-build-"))
async function bundle(name, env, facadeFor) {
	mkdirSync(out, { recursive: true })
	const entry = join(scratch, `${name}-entry.mjs`)
	// The environment is set before bench.mjs evaluates: ES modules evaluate their imports in order.
	writeFileSync(join(scratch, `${name}-env.mjs`), env)
	writeFileSync(entry, `import ${JSON.stringify(join(scratch, `${name}-env.mjs`))}\nimport ${JSON.stringify(benchMjs)}\n`)
	await build({
		entryPoints: [entry], outfile: join(out, `${name}.mjs`), bundle: true, platform: "node", format: "esm", target: "node24",
		packages: "bundle", external: ["node:*"], plugins: [resolver(facadeFor)], logLevel: "warning", legalComments: "eof",
		banner: { js: "import { createRequire as __pigCreateRequire } from 'node:module'; const require = __pigCreateRequire(import.meta.url);" },
	})
}

// The Pi arm's version label names the checkout it bundles: main-<pin> at durable/contract/PIN's main commit, else
// pi-durable's package version and the checkout's commit (or the version alone outside git).
const head = (() => { try { return execFileSync("git", ["-C", pi, "rev-parse", "HEAD"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim() } catch { return undefined } })()
const piVersion = JSON.parse(readFileSync(join(pi, "packages/durable/package.json"), "utf8")).version
const piLabel = head === pin ? `main-${pin.slice(0, 10)}` : head ? `${piVersion}-${head.slice(0, 10)}` : piVersion
await bundle("bench-pi", `process.env.BENCH_TARGET ??= "pi"\nprocess.env.BENCH_DURABLE_VERSION ??= ${JSON.stringify(piLabel)}\n`, false)
await bundle("bench-pig", `import { existsSync, readFileSync } from "node:fs"
import { createHash } from "node:crypto"
import { fileURLToPath } from "node:url"
if (!process.env.DCORE_WASM) {
	const found = [new URL("../dist/core-tinygo.wasm", import.meta.url)].map(u => fileURLToPath(u)).find(p => existsSync(p))
	if (!found) throw new Error("bench-pig: no core module: set DCORE_WASM, or put core-tinygo.wasm in ../dist")
	process.env.DCORE_WASM = found
}
process.env.BENCH_TARGET ??= "pig-tinygo"
process.env.BENCH_DURABLE_VERSION ??= "core-" + createHash("sha256").update(readFileSync(process.env.DCORE_WASM)).digest("hex").slice(0, 16)
`, true)
rmSync(scratch, { recursive: true, force: true })
const sums = ["bench-pi.mjs", "bench-pig.mjs"].map(f => `${createHash("sha256").update(readFileSync(join(out, f))).digest("hex")}  ${f}`)
console.log(sums.join("\n"))
