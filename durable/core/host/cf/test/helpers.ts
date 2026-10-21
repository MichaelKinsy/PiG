import { execFileSync } from "node:child_process"
import { existsSync, mkdtempSync, readFileSync, renameSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

export const root = join(dirname(fileURLToPath(import.meta.url)), "..")
const core = join(root, "..", "..")

/** The toolchain the suite runs against: `DURABLE_CORE_TOOLCHAIN=go` (default) or `tinygo`. The same source builds under both. */
export const toolchain = process.env.DURABLE_CORE_TOOLCHAIN ?? "go"

/** Path of the probe core built with the suite's toolchain, built on demand. */
export function probeWasm(): string {
  const wasm = join(root, "dist", `probe-${toolchain}.wasm`)
  if (!existsSync(wasm)) {
    // Test files run side by side and each calls this: the build goes to a file of its own and takes the name in one rename, so a file that finds the name present reads a whole module, never one another file is still writing.
    const building = wasm.replace(/\.wasm$/, `.${process.pid}.tmp.wasm`)
    try {
      execFileSync(join(core, "budget", "build-wasm.sh"), [toolchain, join(core, "probe", "wasm"), building], { stdio: "inherit" })
      renameSync(building, wasm)
    } finally {
      rmSync(building, { force: true })
    }
  }
  return wasm
}

/** The probe core, built on demand. */
export function probeModule(): WebAssembly.Module {
  return new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(probeWasm()))
}

export const tempDir = (name: string) => mkdtempSync(join(tmpdir(), `do-core-${name}-`))
