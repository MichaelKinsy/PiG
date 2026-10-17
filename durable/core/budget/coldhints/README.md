# coldhints

`coldhints <module.wasm> <coldset.txt>` adds WebAssembly compilation hints for the functions that the first turn of a session runs, so that a host that enables hints compiles them with the module instead of on their first call. `build-wasm.sh` runs it on every TinyGo build, after `patchdata`. Go builds are not hinted.

## What a runtime does with the hints

Both sections are custom sections and every runtime ignores them unless it implements the Wasm compilation hints proposal and enables it. V8 implements it behind `--experimental-wasm-compilation-hints`, which is off by default (measured on V8 12.4, 13.6, 14.1, 14.6, 15.0 and 15.6). Cloudflare Workers cannot set V8 flags, so the hints do nothing there. A Node host opts in with `DURABLE_COMPILE_HINTS=1` (`host/cf/src/compile-hints.ts`).

| V8 | section it reads | flags |
|---|---|---|
| 12 and 13 (Node 22, 24) | `compilationHints`, one byte per declared function | `--experimental-wasm-compilation-hints --no-wasm-lazy-compilation`, synchronous compile |
| 14.0 to 14.5 (Node 25: 14.1) | `metadata.code.compilation_priority`: per function index, byte offset 0, size 1, priority 0 | `--experimental-wasm-compilation-hints`, synchronous compile: on 14.1 the promise of a hinted `WebAssembly.compile` never settles. Node 25 is outside the host's supported range: there the first turn still compiles about 276 functions after the module, so `coldhints.test.ts` fails |
| 14.6 to 15.1 (Node 26) | the same | `--experimental-wasm-compilation-hints` |
| 15.2 and later | the same | `--wasm-compilation-hints` (V8 15.1 added the name and kept the old one as an alias that V8 15.6 rejects; workerd aborts on an unknown V8 flag) |

The priority layout is the one V8's decoder reads (`DecodeCompilationPrioritySection` in `src/wasm/module-decoder-impl.h`, V8 14.0 to 14.6): a function count, then per function its index, byte offset, hint length and priority. The proposal's overview (WebAssembly/compilation-hints) puts a vector of hints after each function index instead, so a V8 that follows the overview will not read this section; `host/cf/test/coldhints.test.ts` fails then. A hint that V8 does not read costs nothing and changes no behavior.

## The cold set

`../coldset.txt` lists name-section names, one per line. The command refuses a list in which fewer than 95% of the names match a function, so a rename cannot drop functions out of the hints unseen. To regenerate the list after a rename or a large change to the turn path:

1. Build the core with the stale list allowed: `COLDHINTS_ARGS=-allow-stale durable/core/budget/build-wasm.sh tinygo durable/core/cmd/corewasm out.wasm`.
2. Run `node --trace-wasm-compilation-times` over the durable bench (`durable/interop/bench.mjs sample`, 50-turn fixture) and over `host/cf/test/coldhints-child.ts` with `DURABLE_COMPILE_HINTS=1`, and keep every `Compiled function ...#<index> using Liftoff` of the core module that is not already compiled with the module.
3. Map each index to its name with the module's name section, and write the sorted union of the new and the matching old names.

`host/cf/test/coldhints.test.ts` builds the core and fails when more than 5% of the functions that an empty-store turn runs are compiled after the module.

The priority section carries no optimization priority, and the build writes no `instr_freq` or `call_targets` section: on V8 15.0 they add 37 KB and no measurable gain (handoffs/research-R6-compile-hints.md). `compileCoreModule` picks the flag from `process.versions.v8` (`hintsFlags`).
