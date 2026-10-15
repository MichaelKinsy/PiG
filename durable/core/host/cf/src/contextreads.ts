// What a request preparation reads of the context the core names (ABI section 4.2, context reads): the summary the core's
// compaction check and prompt planning need, answered from the range the ContextReader keeps.
import type { AssistantMessage, Message } from "@earendil-works/pi-ai"
import type { ContextReader } from "./contextstore.ts"
import { RecipeError } from "./recipe.ts"
import { calculateContextTokens, estimateMessageTokens } from "./tokens.ts"

export type ContextSummary = {
  tail: number
  head: number | null
  /** `[id, kind]` of every active entry, present only with a head marker (prompt planning reads them then). */
  entries?: [number, string][]
  /** The system messages of the view, in order. */
  system: readonly unknown[]
  /** compaction.ts estimateContext(view, []): the usage of the newest measured assistant message plus estimates of the messages after it. */
  tokens: number
}

/** compaction.ts estimateContext without extra messages. */
export function estimateContext(view: {
  readonly head: { readonly id: number } | undefined
  readonly entries: readonly { readonly id: number }[]
  readonly contributions: readonly (readonly Message[])[]
  readonly messages: readonly Message[]
}): number {
  // compaction.ts estimateContext measures the newest assistant message of an entry that is NOT before the head marker by id
  // (a compaction keeps entries older than its marker, so a marker's context lists entries out of id order). Without a head
  // marker that is the last assistant message with usage in the view, and a range resumed from a checkpoint, which holds
  // no entries, can answer it; a marker's context is always read with every entry (answerContextRead).
  let from = 0
  let tokens = 0
  if (view.head === undefined) {
    for (let index = view.messages.length - 1; index >= 0; index--) {
      const message = view.messages[index]!
      if (message.role === "assistant" && calculateContextTokens((message as AssistantMessage).usage) > 0) {
        from = index + 1
        tokens = calculateContextTokens((message as AssistantMessage).usage)
        break
      }
    }
  } else {
    let measured: AssistantMessage | undefined
    for (let index = view.entries.length - 1; index >= 0 && measured === undefined; index--) {
      if (view.entries[index]!.id <= view.head.id) continue
      measured = view.contributions[index]!.findLast(
        (message): message is AssistantMessage => message.role === "assistant" && calculateContextTokens(message.usage) > 0,
      )
    }
    if (measured !== undefined) {
      from = view.messages.lastIndexOf(measured) + 1
      tokens = calculateContextTokens(measured.usage)
    }
  }
  for (let index = from; index < view.messages.length; index++) tokens += estimateMessageTokens(view.messages[index]!)
  return tokens
}


// The statements of the core's sql_table the host answers from its kept ranges instead of SQL (store/sql.go).
export const CONTEXT_SUMMARY = "SELECT pig_context_summary(?, ?, ?, ?, ?)"
export const CONTEXT_MESSAGES = "SELECT pig_context_messages(?, ?, ?, ?)"
export const CONTEXT_RELEASE = "SELECT pig_context_release(?)"

const enc = new TextEncoder()

/** The rows of a context read, or undefined when `text` is not one. Parameters are those of store/lazy.go SQLContextSummary, SQLContextMessages and SQLContextRelease. */
export function answerContextRead(contexts: ContextReader, text: string, params: readonly (null | number | string | Uint8Array)[]): unknown[][] | undefined {
  const n = (i: number) => params[i] as number
  // The marker's head the core named must be the stored marker's: the core captured both from its own state (context.ts captureContextBounds).
  const read = () => {
    // The entries of a head marker's context are listed to the core: such a read needs every entry, not a checkpoint's tail.
    const range = contexts.read(n(0), n(2), n(1), n(2) !== 0 && text === CONTEXT_SUMMARY)
    if (n(2) !== 0 && range.bounds.head?.head !== n(3)) {
      throw new RecipeError(`context read names head marker ${n(2)} with head ${n(3)}; storage has head ${range.bounds.head?.head}`)
    }
    return range
  }
  switch (text) {
    case CONTEXT_SUMMARY: {
      const view = read().view
      const summary: ContextSummary = {
        tail: n(1),
        head: view.head?.id ?? null,
        ...(view.head === undefined ? {} : { entries: view.entries.map((entry): [number, string] => [entry.id, entry.kind]) }),
        system: view.messages.filter((message) => message.role === "system"),
        tokens: n(4) === 1 ? estimateContext(view as unknown as Parameters<typeof estimateContext>[0]) : 0,
      }
      return [[enc.encode(JSON.stringify(summary))]]
    }
    case CONTEXT_MESSAGES: return [[enc.encode(JSON.stringify(read().view.messages))]]
    case CONTEXT_RELEASE: contexts.drop(n(0)); return []
    default: return undefined
  }
}
