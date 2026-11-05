// The host side of the partial patch protocol (core/turn/partial_patch.go): a model stream's partial message goes to the core as what grew
// since the partial sent last, not whole. Serializing the whole message on every model event costs time and memory that follow the length of
// the answer; Pi's partial commit costs the new text (packages/durable/src/harness/partial-changes.ts), and so does this.
//
// A text or thinking block goes as its field emptied and `$a`, the text its deltas added since the last partial sent (when the lengths agree); a tool call as `arguments: {}` and
// `$o`, the chord operations its streamed argument text made, from pi-ai's StreamingJsonParser as Pi's PartialChanges takes them; any block the
// host cannot describe so goes whole, and the core takes it as it is.
import { StreamingJsonParser, type JsonChange } from "@earendil-works/pi-ai/utils/streaming-json"

/** What of a text block was sent: how long it was. A text is never read back: slicing a string that grew by concatenation flattens it, a copy of all of it. */
type SentText = { readonly field: "text" | "thinking"; readonly length: number }
/** The text deltas of a block since the last partial sent. */
type Pending = { readonly field: "text" | "thinking"; text: string }
type Call = { readonly parser: StreamingJsonParser; ops: unknown[] }

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value)

/** One argument change as a chord operation on the call's arguments. */
function operation(change: JsonChange): unknown {
  if (change.kind === "append") return ["a", change.path, change.text]
  return change.path.length === 0 ? ["r", change.value] : ["s", change.path, change.value]
}

export class PartialPatcher {
  private readonly sent = new Map<number, SentText>()
  private readonly pending = new Map<number, Pending>()
  /** Blocks that ended: providers may replace the text when a block ends, so the next partial carries it whole once. */
  private readonly ended = new Set<number>()
  private readonly calls = new Map<number, Call>()

  /** Record what one event of the stream changed: the argument text of a tool call is the part that cannot be read off the partial cheaply. */
  observe(event: any): void {
    switch (event.type) {
      case "text_delta":
      case "thinking_delta": {
        const field = event.type === "text_delta" ? "text" : "thinking"
        const pending = this.pending.get(event.contentIndex)
        if (pending?.field === field) pending.text += event.delta
        else this.pending.set(event.contentIndex, { field, text: event.delta })
        return
      }
      case "text_end":
      case "thinking_end":
        // Providers may replace the text when a block ends: what was collected is no longer an append.
        this.pending.delete(event.contentIndex)
        this.ended.add(event.contentIndex)
        return
      case "toolcall_start":
        this.calls.set(event.contentIndex, { parser: new StreamingJsonParser(), ops: [] })
        return
      case "toolcall_delta": {
        const call = this.calls.get(event.contentIndex)
        if (call === undefined) return
        for (const change of call.parser.append(event.delta)) {
          // Arguments are an object: any other root shows as no arguments, so the call goes whole from then on.
          if (change.path.length === 0 && !(change.kind === "set" && isRecord(change.value))) { this.calls.delete(event.contentIndex); return }
          call.ops.push(operation(change))
        }
        return
      }
      case "toolcall_end":
        // The call's last partial carries its final arguments whole.
        this.calls.delete(event.contentIndex)
        return
    }
  }

  /** The partial to send now: `partial` as a patch of the last one sent. Call it once per partial sent, in order. */
  patch(partial: any): any {
    const content = partial.content
    if (!Array.isArray(content)) return partial
    return { ...partial, content: content.map((block: any, index: number) => this.block(block, index)) }
  }

  private block(block: any, index: number): any {
    if (block?.type === "text" || block?.type === "thinking") {
      const field = block.type as "text" | "thinking"
      const text = block[field]
      if (typeof text !== "string") return block
      const before = this.sent.get(index)
      const pending = this.pending.get(index)
      const ended = this.ended.delete(index)
      this.sent.set(index, { field, length: text.length })
      this.pending.delete(index)
      if (before === undefined || before.field !== field || ended) return block
      // What the deltas add to what was sent is the whole text only when the lengths agree (as Pi's PartialChanges checks): else the block goes whole.
      // No delta since the last partial sent, and the same length: nothing was added.
      const added = pending !== undefined && pending.field === field ? pending.text : ""
      if (before.length + added.length === text.length) return { ...block, [field]: "", $a: added }
      return block
    }
    if (block?.type === "toolCall") {
      const call = this.calls.get(index)
      if (call === undefined) return block
      const ops = call.ops
      call.ops = []
      return { ...block, arguments: {}, $o: ops }
    }
    return block
  }
}
