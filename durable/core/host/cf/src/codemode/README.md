# Codemode on Cloudflare Workers

The Cloudflare entry of codemode: a Durable Object that runs the VM of a remote codemode sandbox. It ports `CodemodeSandboxDurableObject` of Pi's `@earendil-works/pi-codemode/cloudflare` (`packages/codemode/src/cloudflare.ts`) and speaks the same remote protocol, so Pi's `RemoteCodemodeSandbox` is a client of it and so is a PiG `RemoteSandbox` (`codemode.NewRemoteSandbox`) whose `Remote` reaches it. PiG ships no Go transport to a Durable Object: `codemode/workerhost_test.go` drives a `RemoteSandbox` against the module in a Node process, and `test/codemode-go-behavior.test.ts` runs the whole sandbox behavior table that way. A PiG `ServeRemote` serves Pi's client.

## How it works

Scripts run in [QuickJS](https://bellard.org/quickjs/) (`quickjs.wasm` of quickjs-wasi 3.6.2, the module `codemode/assets` embeds). Workers cannot compile Wasm from bytes and wazero is not a TinyGo target, so the Worker path has two static modules:

- `quickjs.wasm`, driven by `quickjs.ts`: one instance per execution, with the clock, random source and interrupt hook QuickJS imports.
- `codemode.wasm`, package `codemode` built with TinyGo (`tools/build-codemode-wasm.sh`, no scheduler, 96 MiB of linear memory at most). It holds the prelude, the VM driver, the interrupt budget and the remote protocol, and calls QuickJS through three synchronous imports (`qjs.call`, `qjs.mem_read`, `qjs.mem_write`) and hands the host each output item through a fourth, `host.output`.

TinyGo's `-scheduler=none` has no goroutines and every import is synchronous, so `serve.ts` owns the asynchrony: the module is a step machine. Each event (the request, a reply to an exchange, a rejected exchange) is one export call that returns the exchanges to send; `serve.ts` awaits them and calls back with the answers. A new pair of instances serves each execution and is dropped when it ends, after an out-of-memory and after a trap, so no linear memory outlives an execution.

The output a script prints is held by the host, not by the module. Each item goes to `serve.ts` as it is printed (`host.output`), and the exchange the module decides on, at 1,000 items, at 1 MiB of characters, at a tool call and at the end, names how many held items it carries. So a trap of the module loses no output: it ends the execution with the crash message behind the items the script printed, as Pi's `serveCodemodeRemote` delivers the output it buffered with a crash. The same holds the items out of the module's 96 MiB heap.

## Use

1. Build the module: `tools/build-codemode-wasm.sh codemode.wasm` (TinyGo 0.42 on `PATH`, or `TINYGO=`).
2. Bundle the Worker with Wrangler's default `CompiledWasm` rule and put `codemode.wasm` and `codemode/assets/quickjs.wasm` (as `quickjs.wasm`) next to `sandbox-object.ts`.
3. Export a class and bind it from the agent's Worker with `script_name`. A separate Worker keeps scripts off the agent's thread:

```ts
import { CodemodeSandboxDurableObject } from "./codemode/sandbox-object.ts"
export class CodemodeSandbox extends CodemodeSandboxDurableObject {
  protected maxInterruptBudget = 200_000
}
```

`maxInterruptBudget` (default 100,000 interrupt polls) and `maxMemoryLimitBytes` (default 32 MiB) apply to every request; a client may ask for less. The Go counterpart is `cloudflare.SandboxDurableObject`.

`test/worker/codemode-do.ts` and `test/codemode-do.test.ts` are a complete agent and sandbox pair run in Miniflare.

## The TypeScript client

`cloudflare.ts` is the Workers entry and `portable.ts` the entry that needs no Node built-ins, as in Pi. They hold Pi's own client code: `execution.ts` (`CodemodeSandboxBase` and the execution it drives), `types.ts` and `identifier.ts` are Pi's files with their import paths changed, and `client.ts` is the client half of Pi's `runtime/remote.ts` (`RemoteCodemodeSandbox`). `InlineCodemodeSandbox` and `createInlineCodemodeSandbox` (`inline.ts`) run the script on the caller's thread, as in Pi, and a script that computes blocks the caller until it awaits a tool or uses up its interrupt budget.

`test/codemode-sandbox.test.ts` and `test/codemode-portable.test.ts` are Pi's `sandbox.test.ts` and `portable.test.ts` with their scripts and expectations, run through `test/vitest-shim.ts` (the vitest API over `node:test`). `test/codemode-do.test.ts` runs the entry's clients in workerd.

## Deterministic runs

`CodemodeSandboxBase.start()` (Pi 1.1.0+0fb1aee7) runs a script with the caller answering its calls, so a durable host can replay it after a restart: UTC, a virtual clock that starts at `startedAt` and moves with the delivered results, randomness from `seed`, no `WeakRef`, results applied in `seq` order, and `started` and `idle` messages. The codemode module implements it (the virtual clock and the seeded `random_get` live in `quickjs.ts` and are set by the module through `qjs.set_clock` and `qjs.set_random_seed`), the client files are Pi's, and `test/codemode-deterministic.test.ts` is Pi's `deterministic.test.ts` on the inline and the remote sandbox with the same pinned random values. Its fingerprint names the prelude of the Go module (Pi's with divergence D145), so a replay on Pi's own VM refuses a run that started here.

## Differences from Pi

- The VM is the codemode module (a TinyGo build of the Go package, QuickJS as a second module), not `quickjs-wasi` driven from TypeScript. So the `wasm` option of `InlineCodemodeSandbox` and of `serveCodemodeRemote` is the pair of compiled modules (`CodemodeModules`), where Pi's is `quickjs.wasm` alone. `createInlineCodemodeSandbox` takes them from Wrangler's `CompiledWasm` imports.
- There is no `CodemodeSandbox` (worker threads): it is in Pi's Node entry, not in `portable` or `cloudflare`, and the Go package has `Sandbox`.
- Pi's `loadQuickJSWasm` and `cloudflareQuickJSWasm` become `cloudflareCodemodeModules`.
