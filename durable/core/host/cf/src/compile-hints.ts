// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Compiling the core on Node with WebAssembly compilation hints. build-wasm.sh marks the functions that the first turn runs
// (budget/coldhints), so that V8 compiles them with Liftoff when it compiles the module instead of one at a time on first call.
// V8 ignores the marks unless the experimental feature is on, and it is off by default in every V8 measured (12.4 to 15.6).
// Cloudflare Workers cannot set V8 flags, so this is for Node hosts only: set DURABLE_COMPILE_HINTS=1 (or pass `hints`) to opt in.
//
// The flags are process-wide and stay set. With hints on, the module compile finishes on V8's own threads and Node does not count
// it as pending work, so on V8 14.6 a promise from WebAssembly.compile that nothing else keeps the event loop alive for never
// settles and the process exits with code 13: the compile here holds a timer until it is done. On V8 14.1 (Node 25) the promise
// of a hinted WebAssembly.compile never settles at all, timer or not, and V8 13 and earlier (Node 24 and earlier) read the marks
// only while lazy compilation is off: below V8 14.6 the compile is synchronous (about 15-35 ms of the main thread, as the marked
// functions compile there and then).

type Compile = { compile(bytes: Uint8Array): Promise<WebAssembly.Module>; Module: new (bytes: Uint8Array) => WebAssembly.Module }

/**
 * The V8 flags that turn hints on. V8 15.1 renamed `--experimental-wasm-compilation-hints` to `--wasm-compilation-hints` and kept
 * the old name as an alias that V8 15.6 no longer accepts; the switch at 15.2 falls where both names work. V8 13 and earlier also
 * need lazy compilation off to read the marks.
 */
export function hintsFlags(v8Version: string): string[] {
  const [major = 0, minor = 0] = v8Version.split(".").map(Number)
  const flag = major > 15 || (major === 15 && minor >= 2) ? "--wasm-compilation-hints" : "--experimental-wasm-compilation-hints"
  return major <= 13 ? [flag, "--no-wasm-lazy-compilation"] : [flag]
}

/** Whether the hinted compile must be synchronous: below V8 14.6 (see the header), V8 13 for the lazy flag and V8 14.1 for a promise that never settles. */
export function hintsCompileSync(v8Version: string): boolean {
  const [major = 0, minor = 0] = v8Version.split(".").map(Number)
  return major < 14 || (major === 14 && minor < 6)
}

/** Whether the environment asks for hints (Node only: DURABLE_COMPILE_HINTS=1). */
export function hintsRequested(): boolean {
  return typeof process !== "undefined" && process.env?.DURABLE_COMPILE_HINTS === "1"
}

/** Compiles the core, with the hints it carries when `hints` is true and this runtime is Node. */
export async function compileCoreModule(bytes: Uint8Array, hints: boolean = hintsRequested()): Promise<WebAssembly.Module> {
  const wasm = WebAssembly as unknown as Compile
  if (!hints || typeof process === "undefined" || !process.versions?.v8) return wasm.compile(bytes)
  const v8 = await import(["node", "v8"].join(":")) as typeof import("node:v8")
  const flags = hintsFlags(process.versions.v8)
  for (const flag of flags) v8.setFlagsFromString(flag)
  if (hintsCompileSync(process.versions.v8)) return new wasm.Module(bytes)
  const hold = setInterval(() => {}, 1000)
  try { return await wasm.compile(bytes) } finally { clearInterval(hold) }
}
