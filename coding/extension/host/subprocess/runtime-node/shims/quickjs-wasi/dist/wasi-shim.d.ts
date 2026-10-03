/**
 * Minimal WASI shim for running QuickJS in any WebAssembly environment.
 *
 * Implements the subset of WASI snapshot_preview1 that QuickJS needs:
 *   - clock_time_get (for Date.now() and Math.random() PRNG seeding)
 *   - fd_write (for QuickJS runtime internal logging)
 *   - fd_close (stub)
 *   - fd_fdstat_get (stub)
 *   - fd_seek (stub)
 *   - random_get (for crypto extension and WASI libc init)
 *
 * Users can override any of these by providing their own implementations
 * in the `wasi` option when creating a VM. Overrides are applied to both
 * the main module and all loaded extensions.
 */
/**
 * WASI host function overrides.
 *
 * A function that receives the WASM linear memory and returns an object of
 * `wasi_snapshot_preview1` functions to override. Any functions returned
 * replace the corresponding built-in implementations. This applies to both
 * the main WASM module and all loaded extensions that import WASI functions.
 *
 * Override functions receive raw WASM linear-memory pointers and must return
 * WASI errno codes (0 = success).
 *
 * @example
 * ```ts
 * // Fixed clock for deterministic Date.now() and Math.random() seeding
 * const vm = await QuickJS.create({
 *   wasi: (memory) => ({
 *     clock_time_get(clockId, precision, resultPtr) {
 *       new DataView(memory.buffer).setBigUint64(resultPtr, 1700000000000n * 1_000_000n, true);
 *       return 0;
 *     },
 *   }),
 * });
 *
 * // Custom RNG that flows to both Math.random() seeding and crypto extension
 * const vm = await QuickJS.create({
 *   wasi: (memory) => ({
 *     random_get(bufPtr, bufLen) {
 *       new Uint8Array(memory.buffer, bufPtr, bufLen).fill(0x42);
 *       return 0;
 *     },
 *   }),
 *   extensions: [{ name: 'crypto', wasm: cryptoSoBytes }],
 * });
 * ```
 */
export type WasiOptions = (memory: WebAssembly.Memory) => Record<string, (...args: any[]) => any>;
/** Returns a wasi_snapshot_preview1 import object with built-in defaults. */
export declare function createWasiShim(memoryAccessor: () => WebAssembly.Memory): {
    clock_time_get(clockId: number, _precision: bigint, resultPtr: number): number;
    fd_write(fd: number, iovsPtr: number, iovsLen: number, nwrittenPtr: number): number;
    fd_close(_fd: number): number;
    fd_fdstat_get(fd: number, statPtr: number): number;
    fd_seek(_fd: number, _offset: bigint, _whence: number, _resultPtr: number): number;
    random_get(bufPtr: number, bufLen: number): number;
};
//# sourceMappingURL=wasi-shim.d.ts.map