// The two compiled modules of the codemode Worker host, for the tests: quickjs-wasi's quickjs.wasm (codemode/assets) and the
// codemode module, which TinyGo builds from package codemode and which is rebuilt when a Go file of the package is newer.
import { execFileSync } from "node:child_process"
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs"
import { join } from "node:path"
import { root } from "./helpers.ts"

const repo = join(root, "..", "..", "..", "..")
export const quickjsWasmPath = join(repo, "codemode", "assets", "quickjs.wasm")
const codemodeSources = [join(repo, "codemode"), join(repo, "codemode", "cloudflare", "cmd", "codemodewasm")]

const newest = () => Math.max(...codemodeSources.flatMap(dir => readdirSync(dir).filter(f => f.endsWith(".go") && !f.endsWith("_test.go")).map(f => statSync(join(dir, f)).mtimeMs)))

/** Path of the codemode module: CODEMODE_WORKER_WASM when it names one, else built on demand. */
export function codemodeWasmPath(): string {
  if (process.env.CODEMODE_WORKER_WASM) return process.env.CODEMODE_WORKER_WASM
  const wasm = join(root, "dist", "codemode.wasm")
  if (!existsSync(wasm) || statSync(wasm).mtimeMs < newest()) execFileSync(join(root, "tools", "build-codemode-wasm.sh"), [wasm], { stdio: "inherit" })
  return wasm
}

const compile = (path: string) => new (WebAssembly.Module as unknown as new (bytes: Uint8Array) => WebAssembly.Module)(readFileSync(path))

/** The modules, compiled once per process, as a Worker compiles them once per isolate. */
export const codemodeModules = (() => {
  let modules: { quickjs: WebAssembly.Module; codemode: WebAssembly.Module } | undefined
  return () => modules ??= { quickjs: compile(quickjsWasmPath), codemode: compile(codemodeWasmPath()) }
})()
