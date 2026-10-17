// Pi 1.1.0+ea448f45 packages/codemode/src/wasm.ts declares the compiled QuickJS module as an opaque object; its Node loader (loadQuickJSWasm) has
// no use on a Worker, where Wrangler's CompiledWasm rule supplies the compiled modules.
/** A compiled `WebAssembly.Module`, typed opaquely because the Node type definitions lack the WebAssembly globals. */
export type CodemodeWasmModule = object
