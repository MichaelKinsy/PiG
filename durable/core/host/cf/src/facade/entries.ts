// pi-durable's entry kinds (src/entries.ts).
export function defineEntry(kind: string): { kind: string; is(entry: { kind?: string } | undefined): boolean } {
  if (typeof kind !== "string" || kind.length === 0) throw new TypeError("Entry kind must be a non-empty string")
  return { kind, is: entry => entry !== undefined && entry.kind === kind }
}
export const UserEntry = defineEntry("pi.user")
export const AssistantEntry = defineEntry("pi.assistant")
export const SystemEntry = defineEntry("pi.system")
export const ToolResultEntry = defineEntry("pi.tool-result")
export const ResetEntry = defineEntry("pi.reset")
export const CompactionEntry = defineEntry("pi.compaction")
