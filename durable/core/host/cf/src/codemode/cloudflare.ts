// Pi 1.1.0+ea448f45 packages/codemode/src/cloudflare.ts: the Cloudflare Workers entry, for Workers built with Wrangler. Two ways to run codemode
// on Cloudflare: `RemoteCodemodeSandbox` with a `CodemodeSandboxDurableObject` deployed as its own Worker (scripts never block the caller), or
// `createInlineCodemodeSandbox` (scripts run on the caller's thread: a script that computes blocks the caller until it awaits a tool or uses up
// its interrupt budget).
import { InlineCodemodeSandbox, type InlineCodemodeSandboxOptions } from "./inline.ts"
import { cloudflareCodemodeModules } from "./sandbox-object.ts"

export * from "./portable.ts"
export { CodemodeSandboxDurableObject, DEFAULT_REMOTE_MEMORY_LIMIT_BYTES, cloudflareCodemodeModules } from "./sandbox-object.ts"

/** `InlineCodemodeSandbox` with the modules Wrangler bundled. */
export function createInlineCodemodeSandbox(options: Omit<InlineCodemodeSandboxOptions, "wasm"> = {}) {
  return new InlineCodemodeSandbox({ ...options, wasm: cloudflareCodemodeModules })
}
