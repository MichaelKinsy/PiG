// Two Durable Objects for the codemode Miniflare tests: the sandbox object under test (default limits, and one with low limits)
// and an agent object that runs a script through it over Workers RPC, with its tools in the agent (Pi's examples/cloudflare).
import { DurableObject } from "cloudflare:workers"
import { CodemodeSandboxDurableObject } from "../../src/codemode/sandbox-object.ts"
import type { CodemodeRemote } from "../../src/codemode/protocol.ts"
import { createInlineCodemodeSandbox, RemoteCodemodeSandbox, type CodemodeTool } from "../../src/codemode/cloudflare.ts"
import { runRemote, type RunOptions, type RunResult, type Tool } from "./codemode-client.ts"

export class SandboxDO extends CodemodeSandboxDurableObject {}

export class LowSandboxDO extends CodemodeSandboxDurableObject {
  protected override maxInterruptBudget = 1000
  protected override maxMemoryLimitBytes = 4 * 1024 * 1024
}

type Env = { SANDBOX: DurableObjectNamespace; LOW: DurableObjectNamespace; AGENT: DurableObjectNamespace }

interface Request {
  sandbox: "SANDBOX" | "LOW"
  name?: string
  code: string
  timeoutMs?: number
  abortAfterMs?: number
  interruptBudget?: number
  memoryLimitBytes?: number
  /** Run the script through the TypeScript client of the Cloudflare entry (cloudflare.ts) instead of the test client. */
  client?: "inline" | "remote"
}

export class AgentDO extends DurableObject<Env> {
  /** Where the tools ran: proves they run in the agent, not in the sandbox. */
  private readonly toolLog: string[] = []

  private tools(): Record<string, Tool> {
    return {
      echo: args => { this.toolLog.push("echo"); return args },
      sleep: async (args, { signal }) => {
        this.toolLog.push("sleep")
        const ms = (args as { ms: number }).ms
        await new Promise<void>((resolve, reject) => {
          const timer = setTimeout(resolve, ms)
          signal.addEventListener("abort", () => { clearTimeout(timer); reject(new Error("aborted")) }, { once: true })
        })
        return ms
      },
      fail: () => { throw new Error("tool failed") },
      where: () => { this.toolLog.push("where"); return { isolate: "agent", toolCalls: this.toolLog.length } },
    }
  }

  private async runWithEntry(request: Request) {
    const tools: CodemodeTool[] = Object.entries(this.tools()).map(([name, execute]) => ({ name, execute }))
    const options = { tools, timeoutMs: request.timeoutMs, interruptBudget: request.interruptBudget, memoryLimitBytes: request.memoryLimitBytes }
    const sandbox = request.client === "inline"
      ? createInlineCodemodeSandbox(options)
      : new RemoteCodemodeSandbox({ ...options, remote: () => (this.env[request.sandbox] as DurableObjectNamespace).getByName(request.name ?? "main") as unknown as CodemodeRemote })
    const controller = new AbortController()
    if (request.abortAfterMs !== undefined) setTimeout(() => controller.abort(), request.abortAfterMs)
    try {
      const result = await sandbox.execute(request.code, { signal: controller.signal })
      return { result: result.ok ? { ok: true as const, value: result.value } : { ok: false as const, error: result.error }, output: result.output, calls: result.calls.map(c => ({ name: c.name, status: c.status })), exchanges: [] as string[][] }
    } finally { await sandbox.close() }
  }

  async run(request: Request): Promise<RunResult & { toolLog: string[] }> {
    const remote = (this.env[request.sandbox] as DurableObjectNamespace).getByName(request.name ?? "main") as unknown as CodemodeRemote
    if (request.client) return { ...(await this.runWithEntry(request)), toolLog: this.toolLog.splice(0) }
    const controller = new AbortController()
    const options: RunOptions = { tools: this.tools(), timeoutMs: request.timeoutMs, interruptBudget: request.interruptBudget, memoryLimitBytes: request.memoryLimitBytes, signal: controller.signal }
    if (request.abortAfterMs !== undefined) setTimeout(() => controller.abort(), request.abortAfterMs)
    const result = await runRemote(remote, request.code, options)
    return { ...result, toolLog: this.toolLog.splice(0) }
  }
}

export default {
  fetch: async (request: globalThis.Request, env: Env) => {
    const body = await request.json() as Request
    const agent = env.AGENT.getByName("agent") as unknown as AgentDO
    return Response.json(await agent.run(body))
  },
}
