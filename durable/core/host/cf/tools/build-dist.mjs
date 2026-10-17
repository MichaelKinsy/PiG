// Build the gate's candidate for one toolchain: dist/<toolchain>/{core.wasm, src/, bench.mjs} (durable/contract/impls.toml).
//
//   node tools/build-dist.mjs <go|tinygo> [--core <main package dir>]
//
// core.wasm is the ABI 1 reactor built from durable/core/cmd/corewasm (budget/build-wasm.sh). src/ is laid out like
// packages/durable/src: index.ts (the facade, with the Wasm module attached) and the storage modules the examples import.
// The templates in candidate/ name the host and the contract directory with @@...@@ markers, replaced here by file paths.
import { execFileSync } from "node:child_process"
import { cpSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs"
import { dirname, join, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const cf = resolve(here, "..")
const repo = resolve(cf, "..", "..", "..", "..")
const toolchain = process.argv[2]
if (toolchain !== "go" && toolchain !== "tinygo") { console.error("usage: build-dist.mjs <go|tinygo> [--core dir]"); process.exit(2) }
const coreArg = process.argv.indexOf("--core")
const core = resolve(coreArg > 0 ? process.argv[coreArg + 1] : join(repo, "durable", "core", "cmd", "corewasm"))
const out = join(cf, "dist", toolchain)

rmSync(out, { recursive: true, force: true })
mkdirSync(join(out, "src"), { recursive: true })
execFileSync(join(repo, "durable", "core", "budget", "build-wasm.sh"), [toolchain, core, join(out, "core.wasm")], { stdio: "inherit" })

const marks = { "@@FACADE@@": join(cf, "src", "facade"), "@@HOST@@": join(cf, "src"), "@@CONTRACT@@": join(repo, "durable", "contract") }
const walk = dir => readdirSync(dir).flatMap(f => (statSync(join(dir, f)).isDirectory() ? walk(join(dir, f)) : [join(dir, f)]))
const template = join(cf, "candidate")
for (const file of walk(template)) {
	const rel = relative(template, file)
	const to = rel === "bench.mjs" ? join(out, rel) : join(out, "src", rel)
	mkdirSync(dirname(to), { recursive: true })
	let text = readFileSync(file, "utf8")
	for (const [mark, value] of Object.entries(marks)) text = text.replaceAll(mark, value)
	writeFileSync(to, text)
}
void cpSync
console.log(`dist/${toolchain}: ${relative(repo, join(out, "core.wasm"))} and the candidate JS API`)
