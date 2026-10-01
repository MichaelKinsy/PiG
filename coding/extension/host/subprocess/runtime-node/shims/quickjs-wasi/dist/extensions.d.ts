/**
 * WASM Dynamic Linking - Extension Loader
 *
 * Loads WASM shared libraries (.so) compiled with wasi-sdk as extensions
 * for the QuickJS WASM runtime. Extensions can call QuickJS C API functions
 * directly through dynamic linking (shared memory + symbol resolution).
 *
 * Key concepts:
 * - Extensions are WASM shared libraries with a `dylink.0` custom section
 * - They share linear memory and the indirect function table with the main module
 * - Symbol imports (env.*) are resolved against the main module's exports
 * - Each extension gets a unique __memory_base and __table_base
 * - Extensions export an init function (e.g., `qjs_ext_url_init`)
 */
/** Parsed content of a dylink.0 custom section */
export interface DylinkInfo {
    memorySize: number;
    memoryAlignment: number;
    tableSize: number;
    tableAlignment: number;
    needed: string[];
}
/** Metadata about a loaded extension, used for snapshot/restore */
export interface LoadedExtension {
    /** Name identifier for the extension */
    name: string;
    /** The compiled WASM module (needed for restore) */
    module: WebAssembly.Module;
    /** The instantiated WASM instance */
    instance: WebAssembly.Instance;
    /** Parsed dylink.0 info */
    dylink: DylinkInfo;
    /** Allocated base offset in linear memory for this extension's static data */
    memoryBase: number;
    /** Allocated base offset in the indirect function table */
    tableBase: number;
    /** Name of the init function exported by the extension */
    initFn: string;
    /** Version info from the extension's qjs_ext_<name>_versions() export (if any) */
    versions?: Record<string, string>;
}
/**
 * The concrete WASI import object shared between the main module and extensions.
 * This is the result of `createWasiShim()` (with any user overrides applied).
 */
export type WasiImports = Record<string, WebAssembly.ImportValue>;
/** Description of an extension to load */
export interface ExtensionDescriptor {
    /** Name identifier (used in snapshot metadata) */
    name: string;
    /** WASM bytes or pre-compiled module */
    wasm: BufferSource | WebAssembly.Module;
    /** Name of the init function exported by the extension (default: `qjs_ext_${name}_init`, with `-` replaced by `_`) */
    initFn?: string;
    /**
     * Extension-provided WASI host function implementations.
     *
     * A factory that receives the WASM linear memory and returns an object of
     * `wasi_snapshot_preview1` functions. These are layered between the built-in
     * defaults and any user-provided overrides:
     *
     *   1. Built-in defaults (lowest priority)
     *   2. Extension-provided (this field)
     *   3. User-provided via `QuickJSOptions.wasi` (highest priority)
     *
     * This allows an extension to ship its own WASI implementations (e.g. a
     * crypto extension providing `random_get`) while still allowing the user
     * to override them at the top level.
     */
    wasi?: (memory: WebAssembly.Memory) => Record<string, (...args: any[]) => any>;
}
/** Parse the dylink.0 custom section from a WASM module */
export declare function parseDylink(module: WebAssembly.Module): DylinkInfo | null;
/**
 * Load a WASM shared library extension and link it against the main module.
 *
 * @param descriptor - Extension description (name, wasm bytes, init function)
 * @param mainExports - The main QuickJS WASM module's exports
 * @param wasiBuiltins - Built-in WASI implementations (lowest priority)
 * @param wasiUserOverrides - User-provided WASI overrides (highest priority)
 * @param memoryProxy - A memory proxy for extension WASI factories
 * @param allocBase - Optional fixed memory/table bases (for snapshot restore)
 */
export declare function loadExtension(descriptor: ExtensionDescriptor, mainExports: Record<string, WebAssembly.ExportValue>, wasiBuiltins?: WasiImports, wasiUserOverrides?: WasiImports, memoryProxy?: WebAssembly.Memory, allocBase?: {
    memoryBase: number;
    tableBase: number;
}): Promise<LoadedExtension>;
/**
 * Call the extension's init function, passing the JSContext and JSRuntime
 * pointers from the main module.
 */
export declare function initExtension(ext: LoadedExtension, mainExports: Record<string, WebAssembly.ExportValue>): void;
/**
 * Re-instantiate extensions for snapshot restore.
 *
 * During restore, we need to:
 * 1. Instantiate extension modules with the same memory/table bases
 * 2. Let them populate the function table (via elem segments and __wasm_apply_data_relocs)
 * 3. Do NOT call the init function (the state is already in the snapshot memory)
 *
 * This reconstructs the function table entries that extensions need, without
 * modifying linear memory (which will be overwritten by the snapshot).
 */
export declare function restoreExtensions(descriptors: ExtensionDescriptor[], extensionMeta: Array<{
    name: string;
    memoryBase: number;
    tableBase: number;
    initFn: string;
}>, mainExports: Record<string, WebAssembly.ExportValue>, wasiBuiltins?: WasiImports, wasiUserOverrides?: WasiImports, memoryProxy?: WebAssembly.Memory): Promise<LoadedExtension[]>;
//# sourceMappingURL=extensions.d.ts.map