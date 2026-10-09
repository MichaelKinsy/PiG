// Lists the imports and exports of a Wasm module and checks them against the ABI tables (ABI section 4.1 and 4.2).
// Usage: node tools/wasm-imports.mjs core.wasm [more.wasm ...]    (V14: run it for every lane's toolchain)
import { readFileSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const abi = JSON.parse(readFileSync(join(here, "..", "..", "..", "abi", "abi.json"), "utf8"))
const allowed = new Set(abi.imports.map(i => i.name))
const required = abi.exports.map(e => e.name)
let bad = 0
for (const file of process.argv.slice(2)) {
  const module = new WebAssembly.Module(readFileSync(file))
  const imports = WebAssembly.Module.imports(module).map(i => `${i.module}.${i.name}`)
  const exports = new Set(WebAssembly.Module.exports(module).map(e => e.name))
  const outside = imports.filter(i => !allowed.has(i))
  const missing = required.filter(e => e !== "_initialize" && !exports.has(e))
  console.log(`${file}: ${imports.length} imports (${imports.join(", ")})`)
  if (outside.length) { bad++; console.log(`  outside the ABI table: ${outside.join(", ")}`) }
  if (missing.length) { bad++; console.log(`  missing exports: ${missing.join(", ")}`) }
}
process.exit(bad ? 1 : 0)
