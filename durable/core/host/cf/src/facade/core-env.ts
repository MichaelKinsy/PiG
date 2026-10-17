// The core module a Node host names in DCORE_WASM when it did not call useCore: the conformance gate's vitest rows import the
// facade without a setup module, so Harness.open finds its core here. DCORE_NATIVE names the native build instead
// (durable/core/cmd/corenative, served through native.ts); one native core process serves every Harness of the run.
import { compileCoreModule } from "../compile-hints.ts"
import type { NativeCore } from "../native.ts"

const loaded = new Map<string, Promise<WebAssembly.Module>>()
let native: Promise<NativeCore> | undefined

export async function coreFromEnvironment(): Promise<WebAssembly.Module | NativeCore | undefined> {
  const exe = typeof process === "undefined" ? undefined : process.env?.DCORE_NATIVE
  if (exe) {
    native ??= (async () => {
      const node = (name: string) => import(["node", name].join(":"))
      const [{ NativeCore }, fs, childProcess, os, path] = await Promise.all([import("../native.ts"), node("fs"), node("child_process"), node("os"), node("path")])
      return new NativeCore(exe, { fs, childProcess, os, path })
    })()
    return native
  }
  const path = typeof process === "undefined" ? undefined : process.env?.DCORE_WASM
  if (!path) return undefined
  let module = loaded.get(path)
  if (!module) {
    module = (async () => {
      // The specifier is computed so a Workers bundle does not resolve node:fs for a path that only exists on Node.
      const { readFileSync } = await import(["node", "fs"].join(":")) as typeof import("node:fs")
      return compileCoreModule(readFileSync(path))
    })()
    loaded.set(path, module)
  }
  return module
}
