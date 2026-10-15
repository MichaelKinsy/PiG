// Type-check the 32 upstream examples against the candidate API: the examples run unmodified (CONTRACT section 6), so every
// export they use must have pi-durable's declaration. The temporary tree is the reference's `packages/durable` with the
// candidate's index.ts and storage modules over it; `@earendil-works/pi-durable` (the facade's type source) maps to the
// reference's own index, so both sides of every type are one declaration tree.
//
//   node tools/check-examples-types.mjs [--keep]      CONTRACT_PI_SOURCE or the gate's cache names the reference checkout
import { execFileSync } from "node:child_process"
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const cf = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const repo = resolve(cf, "..", "..", "..", "..")
const pi = resolve(process.env.CONTRACT_PI_SOURCE ?? join(process.env.DURABLE_CONTRACT_CACHE ?? join(repo, "durable", "contract", ".cache"), "pi-main"))
const durable = join(pi, "packages", "durable")
if (!existsSync(join(durable, "src", "index.ts"))) { console.error(`reference checkout not found at ${pi}; run make durable-contract-setup or set CONTRACT_PI_SOURCE`); process.exit(2) }

const work = mkdtempSync(join(tmpdir(), "durable-examples-types-"))
cpSync(join(durable, "src"), join(work, "src"), { recursive: true })
cpSync(join(durable, "test", "examples"), join(work, "test", "examples"), { recursive: true })
renameSync(join(work, "src", "index.ts"), join(work, "src", "ref-index.ts"))
const marks = { "@@FACADE@@": join(cf, "src", "facade"), "@@HOST@@": join(cf, "src") }
const fill = text => Object.entries(marks).reduce((t, [k, v]) => t.replaceAll(k, v), text)
writeFileSync(join(work, "src", "index.ts"), `export * from ${JSON.stringify(join(cf, "src", "facade", "index.ts"))}\nexport { MemoryStorage } from "./storage/memory.ts"\n`)
for (const rel of ["storage/memory.ts", "storage/sqlite/node.ts", "storage/jsonl/node.ts"]) {
	mkdirSync(dirname(join(work, "src", rel)), { recursive: true })
	writeFileSync(join(work, "src", rel), fill(readFileSync(join(cf, "candidate", rel), "utf8")))
}
const modules = join(cf, "node_modules")
symlinkSync(modules, join(work, "node_modules"))
writeFileSync(join(work, "tsconfig.json"), JSON.stringify({
	compilerOptions: {
		target: "ES2024", module: "ESNext", moduleResolution: "Bundler", strict: true, noEmit: true, allowImportingTsExtensions: true, skipLibCheck: true,
		types: ["node", "workers-types"], typeRoots: [join(modules, "@types"), join(modules, "@cloudflare")], lib: ["ES2024"],
		paths: { "@earendil-works/pi-durable": [join(work, "src", "ref-index.ts")], "@earendil-works/pi-durable/*": [join(durable, "src", "*", "index.ts")] },
	},
	include: ["test/examples/*.ts"],
}))
let status = 0
try {
	execFileSync(join(modules, ".bin", "tsc"), ["-p", work], { stdio: "pipe", encoding: "utf8" })
} catch (e) {
	// Errors in the reference's own sources and in the host under check are reported; only the examples decide the exit status.
	const lines = String(e.stdout).split("\n").filter(l => /error TS/.test(l))
	const mine = lines.filter(l => l.includes("/test/examples/"))
	console.log(lines.join("\n"))
	console.log(`${mine.length} errors in the examples, ${lines.length - mine.length} elsewhere`)
	status = mine.length ? 1 : 0
}
if (process.argv.includes("--keep")) console.log(work)
else rmSync(work, { recursive: true, force: true })
process.exit(status)
