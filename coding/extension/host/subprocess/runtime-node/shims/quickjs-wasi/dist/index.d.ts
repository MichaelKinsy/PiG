/**
 * QuickJS WASM - A snapshotable JavaScript runtime via WebAssembly.
 *
 * Provides a clean JavaScript API for running sandboxed JS code in a QuickJS
 * VM compiled to WASM. The key differentiator is the ability to snapshot the
 * entire VM state (including pending promises) and restore it in a fresh
 * WASM instance.
 */
import { type WasiOptions } from './wasi-shim.js';
import { type ExtensionDescriptor } from './extensions.js';
/**
 * Largest supported QuickJS native stack limit for the shipped WASM binary.
 *
 * The binary has a 1 MiB linker-defined stack; reserving half of it leaves
 * headroom for native frames and stack-overflow exception handling.
 */
export declare const MAX_STACK_SIZE: number;
export type HostFunction = (this: JSValueHandle, ...args: JSValueHandle[]) => JSValueHandle;
/**
 * A batch of handles created inside `vm.withScope()`, disposed together when
 * the scope ends.
 */
export interface HandleScope {
    /**
     * Remove a handle from the scope so that it outlives it. The handle is
     * transferred to the enclosing scope when there is one, otherwise it
     * becomes the caller's responsibility to dispose.
     *
     * Use this for the value you intend to return.
     */
    escape<T extends JSValueHandle>(handle: T): T;
}
/** Property descriptor flags for `defineProp()`. */
export interface JSPropertyDescriptor {
    writable?: boolean;
    enumerable?: boolean;
    configurable?: boolean;
}
/**
 * An own-property descriptor returned by
 * {@link JSValueHandle.getOwnPropertyDescriptor}. Mirrors the result of
 * `Object.getOwnPropertyDescriptor()`: a data property carries `value` +
 * `writable`, an accessor property carries `get` + `set`.
 *
 * The `value`/`get`/`set` handles are owned by the caller and must be
 * disposed.
 */
export interface JSOwnPropertyDescriptor {
    /** Present for data properties. Caller must dispose. */
    value?: JSValueHandle;
    /** Present for accessor properties (may be an `undefined` handle). Caller must dispose. */
    get?: JSValueHandle;
    /** Present for accessor properties (may be an `undefined` handle). Caller must dispose. */
    set?: JSValueHandle;
    /** Present for data properties. */
    writable?: boolean;
    enumerable: boolean;
    configurable: boolean;
}
export type { WasiOptions };
export type { ExtensionDescriptor, LoadedExtension, DylinkInfo, WasiImports } from './extensions.js';
/**
 * Flags for `evalCode()`, matching the QuickJS `JS_EVAL_*` constants.
 */
export declare const EvalFlags: {
    /** Global script mode (default). */
    readonly TYPE_GLOBAL: 0;
    /**
     * Module mode. `evalCode()` returns a handle to a Promise that resolves
     * to the module's namespace object (its exports), or rejects if module
     * evaluation throws. Use together with `executePendingJobs()` and
     * `resolvePromise()`.
     */
    readonly TYPE_MODULE: 1;
    /** Force strict mode. */
    readonly STRICT: 8;
    /** Compile only; do not execute. */
    readonly COMPILE_ONLY: 32;
    /** Omit stack frames before this eval from Error backtraces. */
    readonly BACKTRACE_BARRIER: 64;
    /**
     * Allow top-level `await` in global scripts. When used, `evalCode()`
     * returns a handle to a Promise that resolves to the completion value.
     * Use together with `executePendingJobs()` and `resolvePromise()`.
     */
    readonly ASYNC: 128;
};
/**
 * Flags for `vm.compile()` controlling what is included in the bytecode output.
 * These can be combined with bitwise OR.
 */
export declare const CompileFlags: {
    /** Strip source code from the bytecode (smaller output, no source in errors). */
    readonly STRIP_SOURCE: 16;
    /** Strip debug information (line numbers, etc.) from the bytecode. */
    readonly STRIP_DEBUG: 32;
};
/**
 * Intrinsic flags for `QuickJSOptions.intrinsics` controlling which
 * built-in JavaScript features are available in the VM.
 *
 * By default all intrinsics are enabled. Pass a bitmask of these flags
 * to create a minimal context. For example, omit `Intrinsics.EVAL` to
 * prevent `eval()` usage, or omit `Intrinsics.PROXY` to disallow `Proxy`.
 *
 * `BaseObjects` (Object, Array, Number, String, Boolean, Error, etc.)
 * is always included and cannot be disabled.
 */
export declare const Intrinsics: {
    /** `Date` constructor and prototype methods. */
    readonly DATE: 1;
    /** `eval()` and `Function()` constructor. */
    readonly EVAL: 2;
    /** `RegExp` constructor, prototype methods, and regex literals. */
    readonly REGEXP: 4;
    /** `JSON.parse()` and `JSON.stringify()`. */
    readonly JSON: 8;
    /** `Proxy` and `Reflect`. */
    readonly PROXY: 16;
    /** `Map`, `Set`, `WeakMap`, `WeakSet`. */
    readonly MAP_SET: 32;
    /** `ArrayBuffer`, `TypedArray` variants, `DataView`. */
    readonly TYPED_ARRAYS: 64;
    /** `Promise`, `async`/`await`. */
    readonly PROMISE: 128;
    /** `BigInt`. Note: BigInt is part of BaseObjects in quickjs-ng and cannot be fully removed. */
    readonly BIG_INT: 256;
    /** `WeakRef` and `FinalizationRegistry`. */
    readonly WEAK_REF: 512;
    /** `performance.now()`. */
    readonly PERFORMANCE: 1024;
    /** `DOMException` class. */
    readonly DOM_EXCEPTION: 2048;
    /**
     * `atob()` and `btoa()` global functions. Also pulls in `DOMException` as
     * a dependency (errors thrown by these functions are `DOMException`s).
     */
    readonly ATOB_BTOA: 4096;
    /** All intrinsics enabled (default). */
    readonly ALL: 4294967295;
};
/** Memory usage statistics from the QuickJS runtime. */
export interface MemoryUsage {
    /** Total bytes allocated via malloc */
    mallocSize: number;
    /** Current malloc limit (0 for unlimited) */
    mallocLimit: number;
    /** Total memory used (including overhead) */
    memoryUsedSize: number;
    /** Number of malloc calls */
    mallocCount: number;
    /** Number of memory-using objects */
    memoryUsedCount: number;
    /** Number of atoms */
    atomCount: number;
    /** Atom memory size */
    atomSize: number;
    /** Number of strings */
    strCount: number;
    /** String memory size */
    strSize: number;
    /** Number of objects */
    objCount: number;
    /** Object memory size */
    objSize: number;
    /** Number of properties */
    propCount: number;
    /** Property memory size */
    propSize: number;
    /** Number of shapes */
    shapeCount: number;
    /** Shape memory size */
    shapeSize: number;
    /** Number of JS functions */
    jsFuncCount: number;
    /** JS function memory size */
    jsFuncSize: number;
    /** JS function code size */
    jsFuncCodeSize: number;
    /** Number of PC-to-line mappings */
    jsFuncPc2lineCount: number;
    /** PC-to-line mapping memory size */
    jsFuncPc2lineSize: number;
    /** Number of C functions */
    cFuncCount: number;
    /** Number of arrays */
    arrayCount: number;
    /** Number of fast arrays */
    fastArrayCount: number;
    /** Number of fast array elements */
    fastArrayElements: number;
    /** Number of binary objects (ArrayBuffer, etc.) */
    binaryObjectCount: number;
    /** Binary object memory size */
    binaryObjectSize: number;
}
export interface QuickJSOptions {
    /**
     * WASM module bytes or pre-compiled module.
     *
     * The caller is responsible for loading the WASM binary using whichever
     * mechanism is appropriate for their environment (e.g. `fetch()`,
     * `node:fs/promises`, a bundler-specific import). For convenience the
     * package ships the binary at the `quickjs-wasi/quickjs.wasm` subpath,
     * which can be resolved by bundlers (e.g. Vite's `?url` loader) or read
     * directly from disk.
     */
    wasm: BufferSource | WebAssembly.Module;
    /** Custom WASI function implementations. */
    wasi?: WasiOptions;
    /**
     * Maximum memory the QuickJS runtime can allocate, in bytes.
     * When exceeded, allocations fail and surface as JS exceptions
     * (e.g. `InternalError: out of memory`).
     */
    memoryLimit?: number;
    /**
     * Maximum native stack space QuickJS may consume, in bytes.
     * Must be an integer between 0 and {@link MAX_STACK_SIZE}. Set to 0 to
     * disable the QuickJS stack guard.
     */
    maxStackSize?: number;
    /**
     * Called periodically during JS execution. Return `true` to interrupt
     * the current execution with an `InternalError: interrupted` exception.
     * Useful for implementing execution timeouts or step limits.
     *
     * The handler is called approximately once per JS bytecode instruction,
     * so it should be fast.
     */
    interruptHandler?: () => boolean;
    /**
     * Called when a promise is rejected without a handler, or when a handler
     * is attached to a previously unhandled rejection.
     *
     * @param promise - The rejected promise
     * @param reason - The rejection reason/value
     * @param isHandled - `true` if a handler was just attached (previously unhandled),
     *   `false` if this is a new unhandled rejection
     *
     * Both `promise` and `reason` handles are owned by the caller and will be
     * disposed automatically after the callback returns.
     */
    onUnhandledRejection?: (promise: JSValueHandle, reason: JSValueHandle, isHandled: boolean) => void;
    /**
     * Module loader for ES module `import` statements. When provided, the VM
     * can resolve and load modules.
     *
     * Both callbacks are **synchronous**: they must return their result
     * immediately. The engine calls them from inside the WASM call stack,
     * which cannot be suspended to await a Promise; returning a Promise
     * throws a `TypeError`.
     *
     * For async module sources (e.g. loading over `https://`), either
     * pre-fetch all module sources before evaluating and serve them from a
     * cache, or use the fetch-and-retry pattern: throw from `load` on a
     * cache miss, fetch the missing module on the host, and re-run
     * `evalCode()`. Already-loaded modules are cached by the runtime and
     * are not re-requested. See the "ES Modules" section of the README.
     *
     * Errors thrown by either callback propagate to the guest as the
     * module resolution error.
     */
    moduleLoader?: {
        /**
         * Resolve a module specifier relative to the importing module.
         * Called when an `import` statement is encountered.
         *
         * @param baseName - The name of the module containing the `import` statement
         * @param specifier - The raw specifier string (e.g. `"./foo.js"`, `"lodash"`)
         * @returns The normalized/canonical module name
         *
         * If omitted, specifiers are passed through to `load` unchanged.
         */
        normalize?: (baseName: string, specifier: string) => string;
        /**
         * Load the source code for a module.
         *
         * @param moduleName - The normalized module name (from `normalize`, or the raw specifier)
         * @returns The module source code as a string
         */
        load: (moduleName: string) => string;
    };
    /**
     * Bitmask of `Intrinsics.*` flags controlling which built-in JavaScript
     * features are available. By default all intrinsics are enabled.
     *
     * Example: Create a VM without `eval()` or `Proxy`:
     * ```ts
     * const vm = await QuickJS.create({
     *   intrinsics: Intrinsics.ALL & ~Intrinsics.EVAL & ~Intrinsics.PROXY,
     * });
     * ```
     */
    intrinsics?: number;
    /**
     * Native WASM extensions to load. Each extension is a WASM shared library
     * (.so) compiled with wasi-sdk that links against the QuickJS C API.
     *
     * Extensions are loaded in order and their init functions are called
     * after the QuickJS runtime is initialized. The same extensions (in the
     * same order) must be provided when restoring from a snapshot.
     */
    extensions?: ExtensionDescriptor[];
    /**
     * Controls the timezone offset used by `Date` within the QuickJS sandbox.
     *
     * - **`'host'`** (default): mirrors the host environment's timezone.
     *   `new Date().getTimezoneOffset()` inside the VM will match the host.
     * - **A number**: a fixed UTC offset in **minutes** (e.g. `-480` for UTC-8,
     *   `60` for UTC+1). Note: this follows the `getTimezoneOffset()` sign
     *   convention where *west* of UTC is positive.
     * - **A callback `(time: number) => number`**: called with seconds since
     *   epoch, must return the UTC offset in minutes for that instant. Useful
     *   for DST-aware custom timezone logic. The callback is invoked whenever
     *   QuickJS converts between UTC and local time (e.g. `getHours()`,
     *   `toString()`, `getTimezoneOffset()`), so it may be called multiple
     *   times per Date operation.
     */
    timezoneOffset?: 'host' | number | ((timeSecs: number) => number);
}
interface QuickJSExports {
    memory: WebAssembly.Memory;
    __stack_pointer: WebAssembly.Global;
    __indirect_function_table: WebAssembly.Table;
    _initialize(): void;
    qjs_get_quickjs_version(): number;
    qjs_init(): number;
    qjs_init2(intrinsics: number): number;
    qjs_destroy(): void;
    qjs_eval(codePtr: number, codeLen: number, filenamePtr: number, flags: number): number;
    qjs_compile(codePtr: number, codeLen: number, filenamePtr: number, evalFlags: number, writeFlags: number, outLenPtr: number): number;
    qjs_eval_bytecode(bufPtr: number, bufLen: number): number;
    qjs_new_string(strPtr: number, strLen: number): number;
    qjs_new_number(num: number): number;
    qjs_new_object(): number;
    qjs_new_array(): number;
    qjs_get_undefined(): number;
    qjs_get_null(): number;
    qjs_get_true(): number;
    qjs_get_false(): number;
    qjs_new_big_int64(lo: number, hi: number): number;
    qjs_get_big_int64(valPtr: number, loOutPtr: number, hiOutPtr: number): number;
    qjs_get_float64(valPtr: number): number;
    qjs_get_string(valPtr: number): number;
    qjs_free_cstring(strPtr: number): void;
    qjs_typeof(valPtr: number): number;
    qjs_is_exception(valPtr: number): number;
    qjs_is_undefined(valPtr: number): number;
    qjs_is_null(valPtr: number): number;
    qjs_is_bool(valPtr: number): number;
    qjs_is_number(valPtr: number): number;
    qjs_is_string(valPtr: number): number;
    qjs_is_object(valPtr: number): number;
    qjs_is_array(valPtr: number): number;
    qjs_is_function(valPtr: number): number;
    qjs_is_error(valPtr: number): number;
    qjs_is_promise(valPtr: number): number;
    qjs_is_symbol(valPtr: number): number;
    qjs_is_big_int(valPtr: number): number;
    qjs_is_array_buffer(valPtr: number): number;
    qjs_get_bool(valPtr: number): number;
    qjs_is_proxy(valPtr: number): number;
    qjs_is_map(valPtr: number): number;
    qjs_is_set(valPtr: number): number;
    qjs_is_date(valPtr: number): number;
    qjs_is_regexp(valPtr: number): number;
    qjs_is_weak_ref(valPtr: number): number;
    qjs_is_weak_map(valPtr: number): number;
    qjs_is_weak_set(valPtr: number): number;
    qjs_is_data_view(valPtr: number): number;
    qjs_get_class_id(valPtr: number): number;
    qjs_get_class_name(valPtr: number): number;
    qjs_get_proxy_target(valPtr: number): number;
    qjs_get_proxy_handler(valPtr: number): number;
    qjs_new_symbol(descPtr: number, descLen: number, isGlobal: number): number;
    qjs_get_symbol_description(valPtr: number, descOutPtr: number): number;
    qjs_get_prop_value(objPtr: number, keyPtr: number): number;
    qjs_set_prop_value(objPtr: number, keyPtr: number, valPtr: number): number;
    qjs_new_array_buffer(dataPtr: number, len: number): number;
    qjs_get_array_buffer(valPtr: number, lenOutPtr: number): number;
    qjs_new_uint8_array(dataPtr: number, len: number): number;
    qjs_get_typed_array_buffer(valPtr: number, byteOffsetOutPtr: number, byteLengthOutPtr: number, bytesPerElementOutPtr: number): number;
    qjs_dup_value(valPtr: number): number;
    qjs_free_value(valPtr: number): void;
    qjs_get_string_len(valPtr: number, plenPtr: number): number;
    qjs_has_own_property_value(objPtr: number, keyPtr: number): number;
    qjs_property_is_enumerable_value(objPtr: number, keyPtr: number): number;
    qjs_get_global(): number;
    qjs_get_prop_string(objPtr: number, namePtr: number): number;
    qjs_set_prop_string(objPtr: number, namePtr: number, valPtr: number): number;
    qjs_define_prop_string(objPtr: number, namePtr: number, valPtr: number, flags: number): number;
    qjs_define_prop_value(objPtr: number, keyPtr: number, valPtr: number, flags: number): number;
    qjs_get_prop_uint32(objPtr: number, idx: number): number;
    qjs_set_prop_uint32(objPtr: number, idx: number, valPtr: number): number;
    qjs_get_own_property_names(objPtr: number): number;
    qjs_get_own_property_names_all(objPtr: number): number;
    qjs_get_own_property_keys(objPtr: number): number;
    qjs_get_own_property_descriptor(objPtr: number, keyPtr: number): number;
    qjs_has_own_property(objPtr: number, namePtr: number): number;
    qjs_property_is_enumerable(objPtr: number, namePtr: number): number;
    qjs_get_prototype_of(objPtr: number): number;
    qjs_get_value_ptr(valPtr: number): number;
    qjs_call(funcPtr: number, thisPtr: number, argc: number, argvPtr: number): number;
    qjs_call_constructor(ctorPtr: number, argc: number, argvPtr: number): number;
    qjs_new_host_function(namePtr: number, nameLen: number, argCount: number): number;
    qjs_new_promise(resolveOutPtr: number, rejectOutPtr: number): number;
    qjs_promise_state(promisePtr: number): number;
    qjs_promise_result(promisePtr: number): number;
    qjs_promise_then(promisePtr: number, onFulfilledPtr: number, onRejectedPtr: number): number;
    qjs_promise_mark_as_handled(promisePtr: number): void;
    qjs_is_job_pending(): number;
    qjs_execute_pending_job(): number;
    qjs_get_exception(): number;
    qjs_new_error(): number;
    qjs_throw(valPtr: number): number;
    qjs_set_memory_limit(limit: number): void;
    qjs_set_max_stack_size(size: number): void;
    qjs_set_interrupt_handler(enable: number): void;
    qjs_set_promise_rejection_handler(enable: number): void;
    qjs_set_module_loader(enable: number): void;
    qjs_run_gc(): void;
    qjs_set_gc_threshold(threshold: number): void;
    qjs_get_gc_threshold(): number;
    qjs_compute_memory_usage(outPtr: number): void;
    qjs_get_runtime_ptr(): number;
    qjs_get_context_ptr(): number;
    qjs_set_runtime_and_context(rtPtr: number, ctxPtr: number): void;
    malloc(size: number): number;
    free(ptr: number): void;
    wasm_malloc(size: number): number;
    wasm_free(ptr: number): void;
}
/** Metadata about an extension saved in a snapshot */
export interface SnapshotExtension {
    name: string;
    memoryBase: number;
    tableBase: number;
    initFn: string;
}
export interface Snapshot {
    /** The raw WASM linear memory contents */
    memory: Uint8Array;
    /** The stack pointer value at snapshot time */
    stackPointer: number;
    /** Pointer to JSRuntime in the WASM memory */
    runtimePtr: number;
    /** Pointer to JSContext in the WASM memory */
    contextPtr: number;
    /** Metadata about loaded extensions (empty if none) */
    extensions: SnapshotExtension[];
}
export interface Deferred {
    /** Handle to the QuickJS promise object */
    handle: JSValueHandle;
    /** A host-side Promise that resolves when the QuickJS promise settles */
    settled: Promise<void>;
    /** Resolve the QuickJS promise with a value */
    resolve(value: JSValueHandle): void;
    /** Reject the QuickJS promise with a value */
    reject(value: JSValueHandle): void;
}
export declare class QuickJS {
    private exports;
    private module;
    private instance;
    private encoder;
    private decoder;
    private disposed;
    /** Registry of host callbacks, keyed by function name */
    private hostCallbacks;
    /** Counter for internal-only callbacks (e.g. promise settle handlers) */
    private nextInternalId;
    private interruptHandler;
    private unhandledRejectionHandler;
    private moduleNormalizeHandler;
    private moduleLoadHandler;
    private timezoneOffsetHandler;
    private _global;
    private _versions;
    private _undefined;
    private _null;
    private _true;
    private _false;
    private _ownedHandles;
    /**
     * The innermost active `withScope()` batch, if any. New non-singleton
     * handles register themselves here so they can be freed together.
     * @internal
     */
    _activeScope: Set<JSValueHandle> | null;
    /** Loaded extensions in deterministic order */
    private loadedExtensions;
    private constructor();
    private setInstance;
    /**
     * Version information for the runtime and loaded native libraries.
     * Always includes `"quickjs-wasi"` (the npm package version) and
     * `"quickjs"` (the QuickJS engine version). Extensions may contribute
     * additional entries for their native dependencies (e.g. `"ada"`, `"mbedtls"`).
     */
    get versions(): Record<string, string>;
    /** The global object. Cached; do not dispose. */
    get global(): JSValueHandle;
    /** The undefined value. Cached; do not dispose. */
    get undefined(): JSValueHandle;
    /** The null value. Cached; do not dispose. */
    get null(): JSValueHandle;
    /** The true value. Cached; do not dispose. */
    get true(): JSValueHandle;
    /** The false value. Cached; do not dispose. */
    get false(): JSValueHandle;
    /**
     * Create a fresh QuickJS VM instance.
     *
     * @param options - Optional configuration. Can also pass raw WASM bytes
     *                  directly for backwards compatibility.
     */
    static create(options?: QuickJSOptions | BufferSource | WebAssembly.Module): Promise<QuickJS>;
    /**
     * Restore a QuickJS VM from a snapshot.
     *
     * @param snapshot - The snapshot to restore from.
     * @param options - Optional configuration. Can also pass raw WASM bytes
     *                  directly for backwards compatibility.
     */
    static restore(snapshot: Snapshot, options?: QuickJSOptions | BufferSource | WebAssembly.Module): Promise<QuickJS>;
    /**
     * Serialize a snapshot to a binary buffer for persistent storage.
     *
     * The format includes a versioned header followed by the raw memory.
     * Apply your own compression (gzip, zstd, etc.) on top for smaller
     * storage. The memory compresses well due to its large zero regions.
     *
     * Format (version 1):
     * ```
     * Offset  Size  Field
     * 0       4     Magic: "QJSS" (0x514A5353, big-endian)
     * 4       1     Version: 1
     * 5       3     Reserved (zero)
     * 8       4     Memory size in bytes (u32 little-endian)
     * 12      4     Stack pointer (u32 little-endian)
     * 16      4     Runtime pointer (u32 little-endian)
     * 20      4     Context pointer (u32 little-endian)
     * 24      N     Memory data (N = memory size from offset 8)
     * ```
     */
    static serializeSnapshot(snapshot: Snapshot): Uint8Array;
    /**
     * Deserialize a snapshot from a binary buffer produced by `serializeSnapshot()`.
     */
    static deserializeSnapshot(data: Uint8Array): Snapshot;
    private static normalizeOptions;
    private static applyLimits;
    private static resolveModule;
    private static instantiate;
    /**
     * Called from WASM when a host function is invoked from QuickJS code.
     */
    private handleHostCall;
    /** Write a JS string into WASM memory, returning the pointer. Caller must free. */
    private writeString;
    /** Read a null-terminated C string from WASM memory */
    private readCString;
    /**
     * Check if a result handle is an exception and throw a JSException if so.
     * Used internally by evalCode and callFunction.
     */
    private throwIfException;
    /**
     * Evaluate JavaScript code and return the result as a handle.
     * If the code throws, a `JSException` (which extends `Error`) is thrown
     * on the host side, matching standard JavaScript semantics.
     *
     * @param code - The JavaScript source code to evaluate.
     * @param filename - Optional filename for error stack traces (default `'<eval>'`).
     * @param flags - Optional bitwise OR of `EvalFlags.*` constants.
     *   For example, pass `EvalFlags.ASYNC` to allow top-level `await`; the
     *   returned handle will be a Promise that resolves to the completion value.
     *   With `EvalFlags.TYPE_MODULE` the returned handle is a Promise that
     *   resolves to the module's namespace object (its exports).
     */
    evalCode(code: string, filename?: string, flags?: number): JSValueHandle;
    /**
     * Compile JavaScript source code to bytecode without executing it.
     * The returned `Uint8Array` can be stored, transferred, or later executed
     * with `evalBytecode()`.
     *
     * @param code - The JavaScript source code to compile.
     * @param filename - Optional filename for error stack traces (default `'<compile>'`).
     * @param evalFlags - Optional bitwise OR of `EvalFlags.*` constants.
     *   Use `EvalFlags.TYPE_MODULE` to compile as a module.
     * @param compileFlags - Optional bitwise OR of `CompileFlags.*` constants.
     *   Use `CompileFlags.STRIP_SOURCE` and/or `CompileFlags.STRIP_DEBUG` to
     *   reduce bytecode size.
     */
    compile(code: string, filename?: string, evalFlags?: number, compileFlags?: number): Uint8Array;
    /**
     * Execute previously compiled bytecode (from `compile()`).
     * Returns the evaluation result as a handle.
     *
     * For module bytecode (compiled with `EvalFlags.TYPE_MODULE`), the
     * returned handle is a Promise that resolves to the module's namespace
     * object (its exports).
     *
     * @param bytecode - The bytecode `Uint8Array` from `compile()`.
     */
    evalBytecode(bytecode: Uint8Array): JSValueHandle;
    /**
     * Execute all pending microtask jobs (promise reactions, etc.)
     * Returns the number of jobs executed.
     */
    executePendingJobs(): number;
    /**
     * Explicitly trigger garbage collection. QuickJS runs GC automatically,
     * but this can be useful to reclaim memory at a known point or before
     * taking a snapshot.
     */
    runGC(): void;
    /**
     * The GC threshold in bytes. When allocated memory exceeds this value,
     * garbage collection is triggered automatically. Set to 0 to disable
     * automatic GC.
     */
    get gcThreshold(): number;
    set gcThreshold(threshold: number);
    /**
     * Get detailed memory usage statistics from the QuickJS runtime.
     * Returns counts and sizes for atoms, strings, objects, functions, etc.
     */
    getMemoryUsage(): MemoryUsage;
    /**
     * Get the global object. Prefer the cached `vm.global` property.
     */
    getGlobal(): JSValueHandle;
    /**
     * Create a new QuickJS string value.
     */
    newString(str: string): JSValueHandle;
    /**
     * Create a new QuickJS number value.
     */
    newNumber(num: number): JSValueHandle;
    /**
     * Create a new QuickJS BigInt value.
     */
    newBigInt(val: bigint): JSValueHandle;
    /**
     * Create a new QuickJS object value.
     */
    newObject(): JSValueHandle;
    /**
     * Create a new QuickJS array value.
     */
    newArray(): JSValueHandle;
    /**
     * Create a global symbol (`Symbol.for(description)`).
     * Global symbols with the same description are always the same symbol,
     * even across snapshot/restore.
     */
    newSymbolFor(description: string): JSValueHandle;
    /**
     * Create a new QuickJS ArrayBuffer by copying data from a host buffer.
     */
    newArrayBuffer(data: ArrayBuffer | Uint8Array): JSValueHandle;
    /**
     * Create a new QuickJS Uint8Array by copying data from a host buffer.
     */
    newUint8Array(data: Uint8Array): JSValueHandle;
    /**
     * Get undefined. Prefer the cached `vm.undefined` property.
     */
    getUndefined(): JSValueHandle;
    /**
     * Get null. Prefer the cached `vm.null` property.
     */
    getNull(): JSValueHandle;
    /**
     * Get true. Prefer the cached `vm.true` property.
     */
    getTrue(): JSValueHandle;
    /**
     * Get false. Prefer the cached `vm.false` property.
     */
    getFalse(): JSValueHandle;
    /**
     * Create a new QuickJS function backed by a host callback.
     *
     * When the function is called inside QuickJS, the host callback is invoked
     * with the `this` value and arguments as JSValueHandles.
     */
    newFunction(name: string, fn: HostFunction): JSValueHandle;
    /**
     * Run `fn` with a handle scope: every handle created during the call is
     * disposed when it returns, except those passed to `scope.escape()`.
     *
     * This is the bulk alternative to disposing handles individually, for code
     * that creates many intermediates, such as walking a large value:
     *
     * ```ts
     * const name = vm.withScope((scope) => {
     *   const user = root.getProp('user');       // freed automatically
     *   const profile = user.getProp('profile'); // freed automatically
     *   return scope.escape(profile.getProp('name'));
     * });
     * ```
     *
     * Scopes nest: `escape()` transfers the handle to the enclosing scope when
     * there is one, so it is still cleaned up at the outer boundary.
     *
     * `fn` must be synchronous. Handles created after an `await` are outside
     * the scope, because it closes as soon as `fn` returns.
     *
     * Host callbacks are safe to trigger inside a scope: the `this`/argument
     * handles the trampoline passes to a callback wrap C-owned pointers and
     * are exempt from scope tracking (see `handleHostCall`), so the scope
     * frees only handles the host actually owns. Handles a callback CREATES
     * (including `dup()`s of its arguments) are tracked normally.
     */
    withScope<T>(fn: (scope: HandleScope) => T): T;
    /**
     * Export a handle as a snapshot-portable token.
     *
     * A handle's heap box lives in the VM's linear memory, so a
     * `snapshot()` taken while the handle is alive carries it, and a VM
     * restored from that snapshot has the identical box at the identical
     * offset. `importHandle(token)` on the restored VM (or on this VM)
     * re-materializes an owned handle for the same guest value without
     * evaluating any guest code.
     *
     * Contract:
     * - the handle must stay undisposed until after `snapshot()`; its
     *   box (and the reference it holds) must be part of the memory image;
     * - the token is only meaningful to THIS VM and VMs restored from a
     *   snapshot of it taken while the handle was alive;
     * - `importHandle` duplicates the underlying value (fresh reference,
     *   fresh box), so it can be called any number of times and each
     *   returned handle is independently owned and disposable. The
     *   exported box's own reference is intentionally never released on
     *   restored VMs (one retained reference per VM image, reclaimed
     *   with the VM).
     *
     * The intended use is boot-time capture: snapshot a VM after capturing
     * references to pristine intrinsics but BEFORE evaluating untrusted or
     * user code, then restore per task and import the captured handles,
     * guaranteeing the references predate anything user code patched,
     * without re-running capture code in the restored VM (where user-
     * patched globals could observe it). See vercel/workflow's host-side
     * serde for a worked example.
     */
    exportHandle(handle: JSValueHandle): number;
    /**
     * Re-materialize a handle from a token produced by `exportHandle`,
     * on this VM, or on a VM restored from a snapshot taken while the
     * exported handle was alive. Returns a NEW owned handle (the
     * underlying value's refcount is incremented); dispose it like any
     * other handle. See `exportHandle` for the full contract.
     */
    importHandle(token: number): JSValueHandle;
    /**
     * Create a QuickJS function backed by a host callback whose registration is
     * tied to the returned handle: disposing the handle unregisters the
     * callback.
     *
     * Use this for short-lived callbacks (e.g. a visitor passed to
     * `Map.prototype.forEach`) where the name is an implementation detail.
     * `newFunction()` keeps its callback registered for the lifetime of the VM
     * (by design, so that names can be re-registered after a snapshot is
     * restored), which makes it unsuitable for callbacks created in a loop.
     *
     * The guest must not retain the function past disposal: calling it after
     * the handle is disposed throws, because the callback is gone. Ephemeral
     * functions do not survive snapshot/restore.
     */
    newEphemeralFunction(fn: HostFunction): JSValueHandle;
    /**
     * Remove a host callback registered with `newFunction()` or
     * `registerHostCallback()`. Returns true if a callback was removed.
     *
     * Any QuickJS function still referencing the name will throw when called,
     * so only unregister once the guest can no longer reach it.
     */
    unregisterHostCallback(name: string): boolean;
    /**
     * Create an internal host function that bypasses the duplicate-name check.
     * Used for ephemeral callbacks (promise settle handlers, resolvePromise, etc.)
     * that are not intended to survive snapshot/restore.
     */
    private newInternalFunction;
    /**
     * Create a new promise.
     *
     * Returns a Deferred with:
     * - `handle` - the QuickJS promise object
     * - `settled` - a host Promise that resolves when the QuickJS promise settles
     * - `resolve(value)` - resolve the promise with a QuickJS value
     * - `reject(value)` - reject the promise with a QuickJS value
     */
    newPromise(): Deferred;
    /**
     * Resolve a promise handle. Returns a host-side Promise that resolves
     * with the settled value/error of the QuickJS promise.
     *
     * If the handle is not a promise, it is treated as an already-fulfilled value.
     *
     * The returned host Promise resolves to `{ value: JSValueHandle }` on
     * fulfillment or `{ error: JSValueHandle }` on rejection.
     */
    resolvePromise(promiseHandle: JSValueHandle): Promise<{
        value: JSValueHandle;
    } | {
        error: JSValueHandle;
    }>;
    /**
     * Subscribe to a promise without executing guest code, via quickjs-ng's
     * JS_PromiseThen: no Promise.prototype.then lookup, no Symbol.species.
     * Returns the chained promise. Handler handles are borrowed (caller
     * still owns and disposes them).
     * @internal
     */
    promiseThenRaw(promise: JSValueHandle, onFulfilled: JSValueHandle, onRejected: JSValueHandle): JSValueHandle;
    /**
     * Mark a promise as handled: an eventual (or already-recorded) rejection
     * will not be reported to `onUnhandledRejection`. Useful when the host
     * observes a rejection through other means (e.g. `resolvePromise()`) and
     * wants to suppress the unhandled-rejection callback for it.
     *
     * No-op if the handle is not a promise.
     */
    markPromiseHandled(promise: JSValueHandle): void;
    /**
     * Call a QuickJS function. If the function throws, a `JSException`
     * is thrown on the host side.
     */
    callFunction(func: JSValueHandle, thisVal: JSValueHandle, ...args: JSValueHandle[]): JSValueHandle;
    /**
     * Invoke a QuickJS constructor with `new`, i.e. `new ctor(...args)`.
     * If the constructor throws (including when `ctor` is not a constructor),
     * a `JSException` is thrown on the host side.
     *
     * This is the counterpart to `callFunction` for building values inside
     * the VM from the host, e.g. `new Date(iso)` on a constructor captured
     * before any user code ran.
     */
    construct(ctor: JSValueHandle, ...args: JSValueHandle[]): JSValueHandle;
    /**
     * Internal: call a QuickJS function without throwing on exception.
     * Used by promise plumbing where exceptions are handled differently.
     */
    private callFunctionRaw;
    /**
     * Set a property on an object. Accepts string or JSValueHandle as key.
     * JSValueHandle keys support symbols (including `Symbol.for()`).
     */
    setProp(obj: JSValueHandle, key: string | JSValueHandle, value: JSValueHandle): void;
    /**
     * Define a property on an object with explicit property descriptor flags.
     * Unlike `setProp`, this allows controlling `writable`, `enumerable`, and
     * `configurable` attributes, matching `Object.defineProperty()` semantics.
     * Accepts string or JSValueHandle as key (JSValueHandle keys support symbols).
     *
     * All flags default to `false` when not specified.
     */
    defineProp(obj: JSValueHandle, key: string | JSValueHandle, value: JSValueHandle, descriptor?: JSPropertyDescriptor): void;
    /**
     * Get a property from an object using a JSValueHandle key.
     * Supports symbol keys (including `Symbol.for()`).
     */
    getProp(obj: JSValueHandle, key: JSValueHandle): JSValueHandle;
    /**
     * Get the current exception, if any.
     */
    getException(): JSValueHandle;
    /**
     * Create a new QuickJS Error object.
     * Accepts a string message or a native Error object.
     */
    newError(messageOrError: string | Error): JSValueHandle;
    /**
     * Get the typeof a handle as a string.
     */
    typeof(handle: JSValueHandle): string;
    /**
     * Convert a QuickJS handle to a host JavaScript value.
     * Handles strings, numbers, booleans, null, undefined, bigint, arrays,
     * errors, functions, and plain objects. Circular references in objects
     * are returned as `undefined`.
     */
    dump(handle: JSValueHandle): unknown;
    private _dump;
    /**
     * Convert a host JavaScript value to a QuickJS handle.
     */
    hostToHandle(value: unknown): JSValueHandle;
    /**
     * Snapshot the entire VM state.
     *
     * Returns a snapshot containing the full WASM linear memory. Use
     * `QuickJS.serializeSnapshot()` to convert to a versioned binary
     * buffer for persistent storage.
     */
    snapshot(): Snapshot;
    /**
     * Re-register a host callback after restoring from a snapshot.
     * The name must match the name passed to `newFunction()` before the snapshot.
     */
    registerHostCallback(name: string, fn: HostFunction): void;
    /**
     * Dispose the VM, releasing all references to the WASM instance
     * so it can be garbage collected by the host JS engine.
     */
    dispose(): void;
    /**
     * Support for `using` declarations (Explicit Resource Management).
     * Automatically disposes the VM when it goes out of scope.
     *
     * ```typescript
     * using vm = await QuickJS.create(wasmBytes);
     * vm.evalCode('1 + 2');
     * // vm is automatically disposed here
     * ```
     */
    [Symbol.dispose](): void;
    private assertNotDisposed;
    /** @internal */
    _getExports(): QuickJSExports;
    /** @internal */
    _getMemory(): WebAssembly.Memory;
    /** @internal */
    _writeString(str: string): {
        ptr: number;
        len: number;
    };
    /** @internal */
    _readCString(ptr: number): string;
}
/**
 * An exception thrown from QuickJS code. Extends `Error` so it works with
 * standard error handling (`instanceof Error`, `.message`, `.stack`), and
 * also exposes a `handle` property, a live `JSValueHandle` to the QuickJS
 * exception value, allowing direct inspection of custom properties.
 *
 * The `handle` must be disposed when you're done with it (or use `using`).
 * If the error propagates uncaught, the handle will be cleaned up when the
 * VM is disposed.
 */
export declare class JSException extends Error {
    #private;
    /**
     * A live handle to the QuickJS exception value. You can read custom
     * properties, call methods, etc. Must be disposed when done.
     */
    readonly handle: JSValueHandle;
    /** @internal */
    constructor(handle: JSValueHandle);
    get name(): string;
    set name(v: string);
    get message(): string;
    set message(v: string);
    get stack(): string | undefined;
    set stack(v: string | undefined);
    dispose(): void;
    [Symbol.dispose](): void;
}
/**
 * A handle to a JSValue inside the QuickJS WASM instance.
 */
export declare class JSValueHandle {
    /** The QuickJS VM instance this handle belongs to. */
    readonly vm: QuickJS;
    /** @internal */
    readonly ptr: number;
    private disposed_;
    /**
     * When true, this handle is a cached singleton (e.g. `undefined`, `null`,
     * `true`, `false`, the global object) and `dispose()` is a no-op. This
     * prevents code that routinely disposes handles (such as the object/array
     * branches of `hostToHandle`) from freeing the shared heap `JSValue*` that
     * the cached singleton still references, which would corrupt later reads.
     * @internal
     */
    private readonly singleton;
    /**
     * When true, this handle wraps a `JSValue*` OWNED BY THE C CALLER: the
     * `this`/argument handles the host-call trampoline passes to a host
     * callback (`handleHostCall`). The C side frees those values after the
     * call returns, so `dispose()` is a no-op and the handle is never
     * registered with an active `withScope()` (either would double-free
     * the guest value and corrupt the heap). A callback that needs to
     * retain an argument past its own invocation must `dup()` it; the
     * duplicate takes a fresh reference and behaves like any owned handle.
     * @internal
     */
    private readonly borrowed;
    /**
     * Extra cleanup to run when this handle is disposed. Used by
     * `newEphemeralFunction()` to unregister its host callback.
     * @internal
     */
    _onDispose: (() => void) | undefined;
    constructor(vm: QuickJS, ptr: number, singleton?: boolean, borrowed?: boolean);
    /**
     * Whether this handle wraps a C-owned pointer (host-callback
     * `this`/arguments). Borrowed handles must never be exported as
     * snapshot tokens: the trampoline frees their boxes after the
     * callback returns. @internal
     */
    get _isBorrowed(): boolean;
    /**
     * Whether `dispose()` has been called on this handle.
     *
     * Note that handle methods do not currently guard against use after
     * disposal: reading from a disposed handle reads freed memory. Check this
     * when a handle's lifetime is managed elsewhere (e.g. by `withScope()`).
     */
    get disposed(): boolean;
    get isUndefined(): boolean;
    get isNull(): boolean;
    /**
     * Get the promise state: 0 = pending, 1 = fulfilled, 2 = rejected
     */
    get isBool(): boolean;
    get isNumber(): boolean;
    get isString(): boolean;
    get isSymbol(): boolean;
    get isBigInt(): boolean;
    get isObject(): boolean;
    get isArray(): boolean;
    get isFunction(): boolean;
    get isError(): boolean;
    get isPromise(): boolean;
    get isArrayBuffer(): boolean;
    /**
     * Whether this value is a Proxy exotic object.
     *
     * This is an engine-level check: it never fires proxy traps and cannot
     * be determined (or spoofed) from within guest JavaScript. Use
     * {@link getProxyTarget} / {@link getProxyHandler} to introspect a
     * detected proxy without executing guest code.
     */
    get isProxy(): boolean;
    /**
     * Whether this value is a Map (engine brand check: trap-free,
     * spoof-proof, and unaffected by prototype/constructor mutation).
     * A Proxy wrapping a Map returns false.
     */
    get isMap(): boolean;
    /**
     * Whether this value is a Set (engine brand check: trap-free,
     * spoof-proof, and unaffected by prototype/constructor mutation).
     * A Proxy wrapping a Set returns false.
     */
    get isSet(): boolean;
    /**
     * Whether this value is a Date (engine brand check: trap-free,
     * spoof-proof, and unaffected by prototype/constructor mutation).
     * A Proxy wrapping a Date returns false.
     */
    get isDate(): boolean;
    /**
     * Whether this value is a RegExp (engine brand check: trap-free,
     * spoof-proof, and unaffected by prototype/constructor mutation).
     * A Proxy wrapping a RegExp returns false.
     */
    get isRegExp(): boolean;
    /** Whether this value is a WeakRef (engine brand check). */
    get isWeakRef(): boolean;
    /** Whether this value is a WeakMap (engine brand check). */
    get isWeakMap(): boolean;
    /** Whether this value is a WeakSet (engine brand check). */
    get isWeakSet(): boolean;
    /** Whether this value is a DataView (engine brand check). */
    get isDataView(): boolean;
    /**
     * A numeric identity for the underlying heap value, or 0 for values that
     * are not heap-allocated (numbers, booleans, `null`, `undefined`).
     *
     * Two handles to the same underlying object always report the same
     * identity, and two live handles to different objects always report
     * different identities, so this is the value to key a `Map` on when
     * deduplicating or detecting cycles across handles (`dump()` uses it for
     * exactly that).
     *
     * The identity is only meaningful while the value is alive; it is an
     * address, so it may be reused after every handle to the value has been
     * disposed. Do not persist it, and do not treat it as unforgeable: a
     * number read out of the guest can trivially collide with one.
     */
    get identity(): number;
    /**
     * Extract the value as a boolean, applying JavaScript truthiness
     * (equivalent to `!!value` inside the VM).
     */
    toBoolean(): boolean;
    /**
     * The internal QuickJS class ID of this value, or 0 for non-objects.
     * Useful as a generic engine-level brand when no dedicated `is*`
     * getter exists. Class IDs are stable within a VM instance but are an
     * engine implementation detail, so prefer the dedicated getters.
     */
    get classId(): number;
    /**
     * The engine-level class name of this value, e.g. `"Object"`, `"Map"`,
     * `"Date"`, `"RegExp"`, or the registered name of an extension-defined
     * class like `"URL"`, or `undefined` for non-objects and unnamed
     * internal classes.
     *
     * Unlike `constructorName` (which reads the `constructor` and `name`
     * properties and can therefore fire getters/proxy traps and be spoofed),
     * this is trap-free: it reads the engine's class table directly, never
     * executes guest code, and cannot be forged by reassigning prototypes or
     * constructors. Note that the engine registers the Proxy class under the
     * name `"Object"` (mirroring `Object.prototype.toString`), so use `isProxy`
     * to detect proxies and `getProxyTarget()` to read the target's brand.
     */
    get className(): string | undefined;
    get promiseState(): number;
    /**
     * Get the typeof this value as a string.
     * Returns the same values as the native `typeof` operator.
     */
    get typeof(): string;
    /**
     * Get the length property of this value (for arrays, strings, etc.).
     */
    get length(): number;
    /**
     * Get the constructor name of this object, or undefined if unavailable.
     */
    get constructorName(): string | undefined;
    /**
     * Get the own enumerable string property names (equivalent to Object.keys()).
     */
    keys(): string[];
    /**
     * Get all own property names including non-enumerable ones
     * (equivalent to Object.getOwnPropertyNames()).
     */
    getOwnPropertyNames(): string[];
    /**
     * Get ALL own property keys (strings and symbols), including
     * non-enumerable (equivalent to Reflect.ownKeys()).
     *
     * String keys are returned as strings; symbol keys are returned as
     * JSValueHandles which the caller must dispose.
     *
     * Trap-free for ordinary objects; fires the `ownKeys` trap for a
     * Proxy (check {@link isProxy} first if that matters).
     */
    getOwnPropertyKeys(): Array<string | JSValueHandle>;
    /**
     * Get the own property descriptor for a key WITHOUT invoking getters
     * (equivalent to Object.getOwnPropertyDescriptor()).
     *
     * This is the safe way to inspect a property that may be an accessor:
     * a data property yields `{ value, writable, enumerable, configurable }`,
     * an accessor property yields `{ get, set, enumerable, configurable }`
     * where `get`/`set` are handles to the accessor functions themselves
     * (never invoked). Returns undefined if there is no such own property.
     *
     * The `value`/`get`/`set` handles are owned by the caller and must be
     * disposed.
     *
     * Trap-free for ordinary objects; fires the `getOwnPropertyDescriptor`
     * trap for a Proxy (check {@link isProxy} first if that matters).
     */
    getOwnPropertyDescriptor(key: string | JSValueHandle): JSOwnPropertyDescriptor | undefined;
    /**
     * Check if a property is an own property (equivalent to Object.prototype.hasOwnProperty).
     */
    hasOwnProperty(name: string): boolean;
    /**
     * Check if a property is enumerable (equivalent to Object.prototype.propertyIsEnumerable).
     */
    propertyIsEnumerable(name: string): boolean;
    /**
     * Get the prototype of this object (equivalent to Object.getPrototypeOf()).
     */
    getPrototypeOf(): JSValueHandle;
    /**
     * Get the `[[ProxyTarget]]` of this Proxy without firing any traps.
     * Throws {@link JSException} if this value is not a Proxy; check
     * {@link isProxy} first. Note the target may itself be a Proxy.
     */
    getProxyTarget(): JSValueHandle;
    /**
     * Get the `[[ProxyHandler]]` of this Proxy without firing any traps.
     * Throws {@link JSException} if this value is not a Proxy; check
     * {@link isProxy} first.
     */
    getProxyHandler(): JSValueHandle;
    /**
     * Get a property by name.
     */
    getProp(name: string): JSValueHandle;
    /**
     * Set a property by name.
     */
    setProp(name: string, value: JSValueHandle): void;
    /**
     * Define a property with explicit property descriptor flags.
     * Unlike `setProp`, this allows controlling `writable`, `enumerable`, and
     * `configurable` attributes, matching `Object.defineProperty()` semantics.
     * Accepts string or JSValueHandle as key (JSValueHandle keys support symbols).
     *
     * All flags default to `false` when not specified.
     */
    defineProp(key: string | JSValueHandle, value: JSValueHandle, descriptor?: JSPropertyDescriptor): void;
    /**
     * Extract the value as a number.
     */
    toNumber(): number;
    /**
     * Extract the value as a BigInt.
     */
    toBigInt(): bigint;
    /**
     * Extract the value as an ArrayBuffer (copies from WASM memory).
     * Works on ArrayBuffer values. For typed arrays, gets the underlying buffer.
     */
    toArrayBuffer(): ArrayBuffer;
    /**
     * Extract the value as a Uint8Array (copies from WASM memory).
     * Works on Uint8Array, ArrayBuffer, and other typed array values.
     */
    toUint8Array(): Uint8Array;
    /**
     * Extract the value as a string. Works on any value.
     *
     * For values that are not already strings this performs a JavaScript
     * string conversion, which **executes guest code**: `toString()` /
     * `valueOf()` / `Symbol.toPrimitive` on the value or its prototype chain,
     * and proxy traps. Guard with `isString` when the caller must not run guest
     * code, and call a captured intrinsic (e.g. `URL.prototype.toString` via
     * `vm.callFunction`) when a specific conversion is wanted.
     */
    toString(): string;
    /**
     * Use this handle, then dispose it. Returns the callback's return value.
     */
    consume<T>(fn: (handle: JSValueHandle) => T): T;
    /**
     * Duplicate this handle (increment refcount).
     */
    dup(): JSValueHandle;
    /**
     * Dispose this handle, freeing the heap-allocated JSValue.
     * Safe to call after the VM has been disposed (becomes a no-op).
     */
    dispose(): void;
    /**
     * Support for `using` declarations (Explicit Resource Management).
     * Automatically disposes the handle when it goes out of scope.
     *
     * ```typescript
     * using result = vm.evalCode('1 + 2');
     * console.log(result.toNumber()); // 3
     * // result is automatically disposed here
     * ```
     */
    [Symbol.dispose](): void;
}
//# sourceMappingURL=index.d.ts.map