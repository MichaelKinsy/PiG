import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
const modules = new Map();
/**
 * Read and compile the QuickJS wasm once per path. `path` defaults to the file in the installed
 * `quickjs-wasi` package; pass it when that file lives elsewhere, for example embedded in a Bun
 * compiled executable. A failed load is retried on the next call.
 */
export function loadQuickJSWasm(path) {
    const resolved = path ?? createRequire(import.meta.url).resolve("quickjs-wasi/quickjs.wasm");
    let module = modules.get(resolved);
    if (!module) {
        const { WebAssembly } = globalThis;
        module = readFile(resolved)
            .then((bytes) => WebAssembly.compile(bytes))
            .catch((error) => {
            modules.delete(resolved);
            throw error;
        });
        modules.set(resolved, module);
    }
    return module;
}
//# sourceMappingURL=wasm.js.map