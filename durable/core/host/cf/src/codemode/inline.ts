// Pi 1.1.0+ea448f45 packages/codemode/src/runtime/inline.ts (InlineCodemodeSandbox) and cloudflare.ts (createInlineCodemodeSandbox) on this host.
// The scripts run on the caller's thread, as in Pi: a script that computes blocks the caller until it awaits a tool or uses up its interrupt
// budget. What differs is the VM: Pi runs quickjs-wasi from TypeScript; here the codemode module (the Go package built with TinyGo) drives
// QuickJS and serve.ts steps it, so the sandbox is the remote client over an in-process remote, the same protocol a Durable Object serves.
// `wasm` is therefore the pair of compiled modules (serve.ts CodemodeModules) where Pi's is quickjs.wasm alone.
import { CodemodeSandboxBase, errorMessage, type VmLauncher } from "./execution.ts"
import { remoteLauncher } from "./client.ts"
import { type CodemodeModules, serveCodemodeRemote } from "./serve.ts"
import type { CodemodeSandboxBaseOptions } from "./types.ts"

export const DEFAULT_INTERRUPT_BUDGET = 100_000

export interface InlineCodemodeSandboxOptions extends CodemodeSandboxBaseOptions {
  /** The compiled quickjs.wasm and codemode.wasm modules. On Cloudflare, import them with Wrangler's `CompiledWasm` rule. */
  wasm: CodemodeModules | Promise<CodemodeModules>
  /**
   * Interrupt polls an execution may use before it fails with `kind: "timeout"`. The only limit on a script that computes without awaiting
   * a tool. Default: {@link DEFAULT_INTERRUPT_BUDGET}.
   */
  interruptBudget?: number
}

export function inlineLauncher(wasm: CodemodeModules | Promise<CodemodeModules>, interruptBudget: number = DEFAULT_INTERRUPT_BUDGET): VmLauncher {
  return async (data, events) => {
    let modules: CodemodeModules
    try {
      modules = await wasm
    } catch (error) {
      throw new Error(`Failed to load QuickJS: ${errorMessage(error)}`)
    }
    const remote = () => ({ execute: (request: Parameters<typeof serveCodemodeRemote>[0], exchange: Parameters<typeof serveCodemodeRemote>[1]) => serveCodemodeRemote(request, exchange, { modules, maxInterruptBudget: interruptBudget }) })
    return remoteLauncher(remote, interruptBudget)(data, events)
  }
}

/**
 * Runs each execution's VM on the calling thread. Works in any runtime with WebAssembly and microtasks. A script that computes blocks the
 * caller's thread until it awaits a tool or uses up its interrupt budget, so prefer `RemoteCodemodeSandbox` to keep scripts off a thread that
 * must stay responsive (such as a Durable Object that serves a session).
 */
export class InlineCodemodeSandbox extends CodemodeSandboxBase {
  constructor(options: InlineCodemodeSandboxOptions) {
    super(options, inlineLauncher(options.wasm, options.interruptBudget))
  }
}
