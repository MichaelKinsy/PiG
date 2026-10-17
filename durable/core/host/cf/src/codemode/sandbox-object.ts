// The Durable Object of the Cloudflare entry (Pi's CodemodeSandboxDurableObject, packages/codemode/src/cloudflare.ts): the remote
// end of a remote codemode sandbox. Wrangler's `CompiledWasm` rule turns the two `.wasm` imports into compiled modules, made once
// per isolate at global scope; Workers cannot compile Wasm from bytes. Build ./codemode.wasm with tools/build-codemode-wasm.sh
// and copy codemode/assets/quickjs.wasm next to this file as ./quickjs.wasm.
import { DurableObject } from "cloudflare:workers"
import type { CodemodeRemoteExchange, CodemodeRemoteRequest } from "./protocol.ts"
import { serveCodemodeRemote } from "./serve.ts"
// @ts-expect-error Wrangler supplies the compiled module
import quickjs from "./quickjs.wasm"
// @ts-expect-error Wrangler supplies the compiled module
import codemode from "./codemode.wasm"

/** Interrupt polls one execution may use; the same number as codemode.DefaultInterruptBudget (a Go test holds the two together). */
export const DEFAULT_INTERRUPT_BUDGET = 100_000

/** Default VM memory limit of {@link CodemodeSandboxDurableObject}. Isolates have 128 MB in total. */
export const DEFAULT_REMOTE_MEMORY_LIMIT_BYTES = 32 * 1024 * 1024

/** The compiled modules Wrangler bundled (Pi's `cloudflareQuickJSWasm`, which is quickjs.wasm alone). */
export const cloudflareCodemodeModules = { quickjs: quickjs as WebAssembly.Module, codemode: codemode as WebAssembly.Module }

/**
 * The remote end of a remote codemode sandbox: a Durable Object whose one RPC method runs a script's VM and calls back into the
 * client for every tool call. Deploy it as its own Worker and bind it from the Worker that runs the agent with `script_name`: a
 * class in the same Worker can share an isolate, and therefore a thread, with the objects that call it.
 *
 * The object keeps no state between executions and never touches storage. Limits apply to every request, whatever the client
 * asks for. Override the fields in a subclass:
 *
 *     export class CodemodeSandbox extends CodemodeSandboxDurableObject {
 *       protected maxInterruptBudget = 200_000
 *     }
 */
export class CodemodeSandboxDurableObject<Env = unknown> extends DurableObject<Env> {
  /** Interrupt polls per execution; the client may only ask for less. */
  protected maxInterruptBudget: number = DEFAULT_INTERRUPT_BUDGET
  /** VM memory per execution; the client may only ask for less. */
  protected maxMemoryLimitBytes: number = DEFAULT_REMOTE_MEMORY_LIMIT_BYTES

  /** Called by a remote codemode sandbox over RPC. Resolves when the script has settled or was stopped. */
  execute(request: CodemodeRemoteRequest, exchange: CodemodeRemoteExchange): Promise<void> {
    return serveCodemodeRemote(request, exchange, { modules: cloudflareCodemodeModules, maxInterruptBudget: this.maxInterruptBudget, maxMemoryLimitBytes: this.maxMemoryLimitBytes })
  }
}
