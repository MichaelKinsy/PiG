// The WASI imports of a TinyGo wasip1 reactor that uses neither files nor sockets: arguments and environment are empty,
// standard output and error are discarded, the clock is the host's and random bytes are cryptographic.

export function goWasiImports(memory: () => WebAssembly.Memory, clock: () => number = () => Date.now()): Record<string, Function> {
  const view = () => new DataView(memory().buffer)
  return {
    args_sizes_get: (count: number, size: number) => { view().setUint32(count, 0, true); view().setUint32(size, 0, true); return 0 },
    args_get: () => 0,
    environ_sizes_get: (count: number, size: number) => { view().setUint32(count, 0, true); view().setUint32(size, 0, true); return 0 },
    environ_get: () => 0,
    clock_time_get: (_id: number, _precision: bigint, out: number) => { view().setBigUint64(out, BigInt(Math.trunc(clock())) * 1_000_000n, true); return 0 },
    random_get: (ptr: number, n: number) => { crypto.getRandomValues(new Uint8Array(memory().buffer, ptr, n)); return 0 },
    fd_write: (_fd: number, iovs: number, n: number, written: number) => {
      let total = 0
      for (let i = 0; i < n; i++) total += view().getUint32(iovs + i * 8 + 4, true)
      view().setUint32(written, total, true)
      return 0
    },
    fd_close: () => 0,
    fd_fdstat_get: (fd: number, buf: number) => {
      if (fd > 2) return 8
      new Uint8Array(memory().buffer, buf, 24).fill(0)
      view().setUint8(buf, 2)
      return 0
    },
    fd_fdstat_set_flags: () => 0,
    fd_prestat_get: () => 8,
    fd_prestat_dir_name: () => 8,
    fd_read: (_fd: number, _iovs: number, _n: number, nread: number) => { view().setUint32(nread, 0, true); return 0 },
    fd_seek: () => 70,
    sched_yield: () => 0,
    proc_exit: (code: number) => { throw new Error(`the codemode module called proc_exit(${code})`) },
  }
}
