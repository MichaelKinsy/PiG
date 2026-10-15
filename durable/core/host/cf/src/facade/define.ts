// Identity helpers that type extension code (src/harness/define.ts, src/tasks.ts). Pure: they never touch the core.
import type { HostExtension, HostSection, HostTask, HostTool, HostHook, HostWrap } from "../registry.ts"

export function defineExtension<T extends HostExtension>(extension: T): T { return extension }
export function defineTool<T extends HostTool>(tool: T): T { return tool }
export function section(key: string, render: HostSection["render"], options?: { tag?: boolean }): HostSection {
  return options?.tag === undefined ? { key, render } : { key, render, tag: options.tag }
}
export function hook(task: { definition: { name: string } }, handlers: HostHook["handlers"]): HostHook { return { task: task.definition.name, handlers } }
export function wrapTool<T extends HostTool>(tool: T, wrapper: (tool: T) => T): HostWrap { return { tool: tool.name, wrap: wrapper } }
export function wrapSection(key: string, wrapper: (section: HostSection) => HostSection): HostWrap { return { section: key, wrap: wrapper } }
export function defineTask<T extends HostTask["definition"]>(definition: T): { definition: T } { return { definition } }

/** The built-in tasks the core runs. Extensions name them in `hook()` and read their results; the phases live in the core. */
const builtin = (name: string) => ({ definition: { name, version: 1 } })
export const GenerationTask = builtin("pi.generation")
/** tool.ts ToolTask, version 2: version 1 had only model-issued calls, so its input becomes `{ kind: "model", ...input }` and its checkpoint stays. */
export const ToolTask = { definition: { name: "pi.tool", version: 2, initial: () => ({ phase: "call" }), migrate: (input: unknown, checkpoint: unknown) => ({ input: { kind: "model", ...(input as object) }, checkpoint }) } }
export const CompactionTask = builtin("pi.compaction")
