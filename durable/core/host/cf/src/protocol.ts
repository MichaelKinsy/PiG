// The JSON payloads the CF host sends and expects (ABI sections 5-8). ABI.md fixes the event, effect and notice kinds
// and the effect payload fields; this file is the host's reading of the rest and the only place that changes when the core
// owners ratify a shape (durable/core/host/PROTOCOL.md lists every field and who owns it).

export type ModelRef = { provider: string; modelId: string }

/** model_context effect (ABI section 6, effect 3). In the delta form `extends` names the effect whose message list this one continues, `append` holds the new messages, and `context` is absent. */
export type ModelContextEffect = {
  model: ModelRef
  context?: { messages: unknown[] }
  extends?: number
  append?: unknown[]
  options?: Record<string, unknown>
}

/** tool effect (effect 6). `handler` indexes the registry's handler table; hosts fall back to the first installed tool of that name. */
export type ToolEffect = {
  taskId: number
  conversationId: number
  toolName: string
  callId: string
  arguments: unknown
  outputWindow?: { maxBytes?: number; maxLines?: number; retain?: "head" | "tail" } | null
  replay: "safe" | "unsafe"
  handler?: number
}

/** hook effect (effect 7): the core runs the chain rules of §7.2 and asks for one handler at a time. */
export type HookEffect = { name: string; handler: number; conversationId: number; taskId: number; payload: Record<string, unknown> }

/** phase effect (effect 8): a custom task's phase handler, or its abort handler when `abort` is true. */
export type PhaseEffect = { taskId: number; kind: string; phase?: string; record: unknown; invocation: number; abort?: true }

/** env effect (effect 9): build HarnessOptions.env for a task; the result stays in the host under `taskId` until the task ends. */
export type EnvEffect = { conversationId: number; taskId?: number; cwd?: string }

/** section effect (effect 11). */
export type SectionEffect = { conversationId: number; taskId: number; section: number; input: Record<string, unknown> }

/** deferred effect (effect 12). */
export type DeferredEffect = { op: "fetch" | "cancel"; model: ModelRef; handle: unknown }

/** callback effect (effect 13): host code Pi runs inside an open commit callback, with the section 8 channel bound to `txId`. */
export type CallbackEffect = { name: string; txId: number; payload: Record<string, unknown> }

/** The positional arguments each hook of §7.2 receives before `(api, context)`, from the effect's `payload`. */
export const HOOK_ARGUMENTS: Record<string, (payload: Record<string, any>) => unknown[]> = {
  beforeRequest: p => [{ messages: p.messages }],
  afterResponse: p => [p.message],
  onYield: p => [p.answer],
  afterTools: p => [p.assistant, p.results],
  beforeTool: p => [p.call],
  afterTool: p => [p.call, p.result],
  beforeCompact: p => [p.compaction],
  prepareArguments: p => [p.arguments],
}

/** Errors cross the host boundary as `{name, message}` (ABI section 8.1 answers, hook_done thrown outcomes). */
export type WireError = { name: string; message: string; stack?: string }

export const wireError = (error: unknown): WireError => {
  const e = error as { name?: unknown; message?: unknown; stack?: unknown }
  return {
    name: typeof e?.name === "string" ? e.name : "Error",
    message: typeof e?.message === "string" ? e.message : String(error),
    ...(typeof e?.stack === "string" ? { stack: e.stack } : {}),
  }
}

/** A document address (ABI section 8.1): `{scope, definition, conversationId?, taskId?, key?}`. */
export type DocAddress = { scope: "session" | "conversation" | "task"; definition: string; conversationId?: number; taskId?: number; key?: unknown }

/** The api_result body: `{ok: value}` or `{error: {name, message}}`. */
export type ApiAnswer = { ok: unknown } | { error: WireError }

/** tx_result bodies (ABI section 8): the line is held (`ready`), queued (`waiting`), an operation's answer, or Pi's rejection. */
export type TxAnswer = { ready: true } | { waiting: true } | { result: unknown } | { rejected: WireError }

export type PublishedNotice = {
  submissions?: { id?: number; requestId: string; status: string }[]
  entries?: number[]
  tasks?: { id: number; kind: string; status: string }[]
  documents?: unknown
  watchId?: number
  frame?: unknown
}
