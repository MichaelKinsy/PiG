// The host's view of a pi-durable registry: the JSON snapshot the core receives (open and registry events, ABI section 5)
// and the append-only table of host functions its `handler` indexes name. An index never changes meaning, so an effect
// issued under an older snapshot still resolves after a reload (spec §7.5: handover at the next phase boundary).

/** What the host needs of a tool, section, hook or task definition: structural, so the pi-durable types and plain objects fit. */
export type HostTool = { name: string; description?: string; parameters?: unknown; replay?: "safe" | "unsafe"; executionMode?: string; outputLimits?: unknown; prepareArguments?: (args: unknown) => unknown; execute: (...args: any[]) => unknown }
export type HostSection = { key: string; tag?: boolean; render: (...args: any[]) => unknown }
export type HostHook = { task: string; handlers: Record<string, ((...args: any[]) => unknown) | undefined> }
export type HostWrap = { tool: string; wrap: (tool: any) => any } | { section: string; wrap: (section: any) => any }
export type HostTask = { definition: { name: string; version?: number; phases?: Record<string, unknown>; abort?: unknown; [key: string]: unknown } }
export type HostExtension = { name: string; tools?: readonly HostTool[]; sections?: readonly HostSection[]; hooks?: readonly HostHook[]; wraps?: readonly HostWrap[]; tasks?: readonly HostTask[] }

export interface HostRegistrySource {
  snapshot(): { installed(): readonly HostExtension[]; task(name: string): HostTask | undefined; tasks(): readonly HostTask[] }
  subscribe(listener: () => void): () => void
}

export type SnapshotJSON = {
  extensions: {
    name: string
    tools: { name: string; description: string; parameters: unknown; replay: "safe" | "unsafe"; handler: number; executionMode?: string; outputLimits?: unknown; prepare?: number }[]
    sections: { key: string; tag: boolean; handler: number }[]
    hooks: { task: string; handlers: Record<string, number> }[]
    wraps: ({ tool: string; handler: number } | { section: string; handler: number })[]
    tasks: { name: string; version: number; phases: string[]; abort: boolean; handler: number }[]
  }[]
}

export class HostRegistry {
  private readonly table: unknown[] = []
  private readonly ids = new Map<unknown, number>()
  private readonly source: HostRegistrySource

  constructor(source: HostRegistrySource) { this.source = source }

  /** The handler index of a host object, assigned on first sight. */
  id(object: unknown): number {
    let id = this.ids.get(object)
    if (id === undefined) { id = this.table.length; this.table.push(object); this.ids.set(object, id) }
    return id
  }

  /** The host object at an index; throws on an index this registry never issued (a core and host that disagree). */
  at<T = unknown>(index: number): T {
    if (!Number.isInteger(index) || index < 0 || index >= this.table.length) throw new Error(`handler ${index} is not in the registry table`)
    return this.table[index] as T
  }

  snapshot(): SnapshotJSON {
    return {
      extensions: this.source.snapshot().installed().map(extension => ({
        name: extension.name,
        tools: (extension.tools ?? []).map(tool => ({
          name: tool.name,
          description: tool.description ?? "",
          parameters: tool.parameters ?? {},
          replay: tool.replay ?? "unsafe",
          handler: this.id(tool),
          ...(tool.executionMode === undefined ? {} : { executionMode: tool.executionMode }),
          ...(tool.outputLimits === undefined ? {} : { outputLimits: tool.outputLimits }),
          ...(tool.prepareArguments === undefined ? {} : { prepare: this.id(tool.prepareArguments) }),
        })),
        sections: (extension.sections ?? []).map(section => ({ key: section.key, tag: section.tag ?? true, handler: this.id(section) })),
        hooks: (extension.hooks ?? []).map(hook => ({
          task: hook.task,
          handlers: Object.fromEntries(Object.entries(hook.handlers).filter(([, fn]) => typeof fn === "function").map(([name, fn]) => [name, this.id(fn)])),
        })),
        wraps: (extension.wraps ?? []).map(wrap => "tool" in wrap ? { tool: wrap.tool, handler: this.id(wrap.wrap) } : { section: wrap.section, handler: this.id(wrap.wrap) }),
        tasks: (extension.tasks ?? []).map(task => ({
          name: task.definition.name,
          version: task.definition.version ?? 1,
          phases: Object.keys(task.definition.phases ?? {}),
          abort: typeof task.definition.abort === "function",
          handler: this.id(task),
        })),
      })),
    }
  }

  /** The tool registration an effect names: by handler index when the core sent one, else the first installed tool of that name. */
  tool(name: string, handler?: number): HostTool {
    if (handler !== undefined) return this.at<HostTool>(handler)
    for (const extension of this.source.snapshot().installed()) for (const tool of extension.tools ?? []) if (tool.name === name) return tool
    throw new Error(`tool ${name} is not installed`)
  }

  task(name: string): HostTask {
    const task = this.source.snapshot().task(name)
    if (!task) throw new Error(`task ${name} is not installed`)
    return task
  }

  subscribe(listener: () => void): () => void { return this.source.subscribe(listener) }
}
