import { execFileSync } from "node:child_process"
import { existsSync, mkdtempSync, readFileSync } from "node:fs"
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
  if (!existsSync(wasm)) execFileSync(join(core, "budget", "build-wasm.sh"), [toolchain, join(core, "probe", "wasm"), wasm], { stdio: "inherit" })
  return wasm
}

/** The probe core, built on demand. */
export function probeModule(): WebAssembly.Module {
  return new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(probeWasm()))
}

export const tempDir = (name: string) => mkdtempSync(join(tmpdir(), `do-core-${name}-`))
