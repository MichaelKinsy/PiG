// Document tokens and addresses: the pure part of pi-durable's documents.ts (defineDoc, defineDocFamily, resolveAddress,
// addressId), which a host needs to name a document in an `api` or `tx` request. Storage-side validation stays in the core.
import type { JsonValue } from "@earendil-works/chord"

export type DocAddressScope = { kind: "session" } | { kind: "conversation"; conversationId: number } | { kind: "task"; taskId: number }
export type DocumentAddress = { kind: string; scope: DocAddressScope; key?: string }

export type AnyDocDefinition = {
  scope: "session" | "conversation" | "task"
  history?: "latest" | "rewindable"
  fork?: string
  kind: string
  version: number
  family?: true
  initial(seed?: JsonValue): Record<string, JsonValue>
  migrate?(value: Record<string, JsonValue>, fromVersion: number): Record<string, JsonValue>
  checkpointWhen?(value: unknown, ops: readonly unknown[], info: unknown): boolean
}
export type AnyDocToken = { readonly definition: AnyDocDefinition }

function validate(definition: AnyDocDefinition): void {
  if (!Number.isSafeInteger(definition.version) || definition.version < 1) {
    throw new TypeError(`Document ${definition.kind} version must be a positive integer`)
  }
}

/** Define a singleton document (any scope). Typed overloads come from pi-durable's declarations; this is the runtime. */
export function defineDoc(definition: any): any {
  validate(definition)
  return { definition }
}

/** Define a document family. */
export function defineDocFamily(definition: any): any {
  validate(definition)
  return { definition }
}

export type ResolvedAddress = { address: DocumentAddress; id: string; nextArgument: number }

/** Resolve an overloaded argument list and return the index after the owner and family key (documents.ts `resolveAddress`). */
export function resolveAddress(definition: AnyDocDefinition, args: readonly unknown[]): ResolvedAddress {
  let index = 0
  let scope: DocAddressScope
  const owner = (what: string): number => {
    const value = args[index++]
    if (typeof value !== "number" || !Number.isSafeInteger(value)) throw new TypeError(`Document ${definition.kind} requires a ${what} ID`)
    return value
  }
  switch (definition.scope) {
    case "session": scope = { kind: "session" }; break
    case "conversation": scope = { kind: "conversation", conversationId: owner("conversation") }; break
    case "task": scope = { kind: "task", taskId: owner("task") }; break
  }
  const key = definition.family === true ? (args[index++] as string) : undefined
  const address: DocumentAddress = key === undefined ? { kind: definition.kind, scope } : { kind: definition.kind, scope, key }
  return { address, id: addressId(address), nextArgument: index }
}

/** Stable string identity of one logical address. */
export function addressId(address: DocumentAddress): string {
  const owner = address.scope.kind === "session" ? null : address.scope.kind === "conversation" ? address.scope.conversationId : address.scope.taskId
  return JSON.stringify([address.kind, address.scope.kind, owner, address.key ?? null])
}

/** The wire form of an address in `api` requests: `{scope, definition, conversationId?, taskId?, key?}` (ABI section 8.1). */
export function wireAddress(address: DocumentAddress) {
  return {
    scope: address.scope.kind,
    definition: address.kind,
    ...(address.scope.kind === "conversation" ? { conversationId: address.scope.conversationId } : {}),
    ...(address.scope.kind === "task" ? { taskId: address.scope.taskId } : {}),
    ...(address.key === undefined ? {} : { key: address.key }),
  }
}
