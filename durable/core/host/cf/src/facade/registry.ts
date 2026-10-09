// An application-owned registry of extensions (src/harness/registry.ts).
import type { HostExtension, HostRegistrySource, HostTask } from "../registry.ts"
import { CompactionTask, GenerationTask, ToolTask } from "./define.ts"
import { INSTRUCTIONS_KEY } from "./agent.ts"

const SECTION_KEY = /^[a-z][a-z0-9_-]*$/
const BUILTIN: readonly HostTask[] = [GenerationTask, ToolTask, CompactionTask]

class State {
  readonly extensions: readonly HostExtension[]
  private readonly byName: Map<string, HostExtension>
  private readonly tasksByName: Map<string, HostTask>
  constructor(extensions: readonly HostExtension[]) {
    this.extensions = extensions
    this.byName = new Map(extensions.map(x => [x.name, x]))
    const tasks = new Map<string, HostTask>()
    for (const t of BUILTIN) tasks.set(t.definition.name, t)
    for (const x of extensions) for (const t of x.tasks ?? []) {
      if (tasks.has(t.definition.name)) throw new Error(`Task ${t.definition.name} of extension ${x.name} is already installed`)
      tasks.set(t.definition.name, t)
    }
    this.tasksByName = tasks
  }
  installed() { return this.extensions }
  extension(name: string) { return this.byName.get(name) }
  tools() { return this.extensions.flatMap(extension => (extension.tools ?? []).map(tool => ({ extension, tool }))) }
  sections() { return this.extensions.flatMap(extension => (extension.sections ?? []).map(section => ({ extension, section }))) }
  tasks() { return [...this.tasksByName.values()] }
  task(name: string) { return this.tasksByName.get(name) }
}

export interface Registry extends HostRegistrySource {
  snapshot(): State
  install(extension: HostExtension): void
  uninstall(extension: HostExtension): void
}

function validate(extension: HostExtension) {
  const tools = new Set<string>()
  for (const tool of extension.tools ?? []) {
    if (tools.has(tool.name)) throw new Error(`Extension ${extension.name} has two tools named ${tool.name}`)
    tools.add(tool.name)
  }
  const sections = new Set<string>()
  for (const { key } of extension.sections ?? []) {
    if (!SECTION_KEY.test(key)) throw new TypeError(`Section key ${JSON.stringify(key)} must match ${SECTION_KEY}`)
    if (key === INSTRUCTIONS_KEY) throw new Error(`Section key ${key} is reserved for the agent's instructions`)
    if (sections.has(key)) throw new Error(`Extension ${extension.name} has two sections with key ${key}`)
    sections.add(key)
  }
}

export function createRegistry(): Registry {
  let current = new State([])
  const listeners = new Set<() => void>()
  const publish = (extensions: readonly HostExtension[]) => { current = new State(extensions); for (const l of [...listeners]) l() }
  return {
    snapshot: () => current,
    subscribe: l => { listeners.add(l); return () => { listeners.delete(l) } },
    install(extension) {
      validate(extension)
      const installed = current.installed()
      const i = installed.findIndex(x => x.name === extension.name)
      publish(i < 0 ? [...installed, extension] : installed.map((x, at) => at === i ? extension : x))
    },
    uninstall(extension) {
      const installed = current.installed()
      if (!installed.some(x => x.name === extension.name)) return
      publish(installed.filter(x => x.name !== extension.name))
    },
  }
}
