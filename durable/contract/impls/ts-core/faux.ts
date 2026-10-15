// The text functions of pi-ai 1.0.4's faux provider (src/providers/faux.ts: contentToText, assistantContentToText,
// toolResultToText, messageToText, serializeContext), reduced to the three numbers the core keeps per message: the role,
// the length of "<role>:<text>", and the tool count the scripted model reads from a user message.
export const ROLE_USER = 1, ROLE_ASSISTANT = 2, ROLE_TOOL_RESULT = 3, ROLE_SYSTEM = 4

type Block = { type: string; text?: string; thinking?: string; name?: string; mimeType?: string; data?: string; arguments?: unknown }
type Content = string | Block[]

/** mirrors /tools=(\d+)/.exec(text)?.[1] ?? 0 */
export function parsePlan(text: string) {
  const m = /tools=(\d+)/.exec(text)
  return m ? Number(m[1]) : 0
}

const blockText = (b: Block) => b.type === "text" ? b.text! : `[image:${b.mimeType}:${b.data!.length}]`

/** contentToText(content).length */
function contentLength(content: Content) {
  if (typeof content === "string") return content.length
  let n = 0
  for (let i = 0; i < content.length; i++) {
    const b = content[i]!
    n += b.type === "text" ? b.text!.length : `[image:${b.mimeType}:${b.data!.length}]`.length
    if (i) n++
  }
  return n
}

/** @earendil-works/pi-ai text.ts contentText(content): text blocks joined by a newline. */
const contentText = (content: Content) => typeof content === "string" ? content : content.filter(b => b.type === "text").map(b => b.text!).join("\n")

function systemText(m: { content?: Content; sections?: Record<string, string | null>; toolsRemoved?: unknown[]; toolsAdded?: unknown[] }) {
  const parts = [contentText(m.content ?? "")]
  for (const t of Object.values(m.sections ?? {})) if (t !== null) parts.push(t)
  const system = parts.filter(p => p.length > 0).join("\n\n")
  return [system, ...(m.toolsRemoved?.map(t => `tool-:${JSON.stringify(t)}`) ?? []), ...(m.toolsAdded?.map(t => `tool+:${JSON.stringify(t)}`) ?? [])].filter(p => p.length > 0).join("\n")
}

export interface MessageInfo { role: number; chars: number; plan: number }

export function messageChars(m: any): MessageInfo {
  switch (m.role) {
    case "user": {
      const c = m.content as Content
      const text = typeof c === "string" ? c : c.filter(b => b.type === "text").map(b => b.text).join("")
      return { role: ROLE_USER, chars: 4 + 1 + contentLength(c), plan: parsePlan(text) }
    }
    case "assistant": {
      const c = m.content as Block[]
      let n = 0
      for (let i = 0; i < c.length; i++) {
        const b = c[i]!
        if (i) n++
        n += b.type === "text" ? b.text!.length : b.type === "thinking" ? b.thinking!.length : `${b.name}:${JSON.stringify(b.arguments)}`.length
      }
      return { role: ROLE_ASSISTANT, chars: "assistant".length + 1 + n, plan: 0 }
    }
    case "toolResult": {
      const c = m.content as Block[]
      let n = (m.toolName as string).length
      for (const b of c) n += 1 + blockText(b).length
      return { role: ROLE_TOOL_RESULT, chars: "toolResult".length + 1 + n, plan: 0 }
    }
    case "system":
      return { role: ROLE_SYSTEM, chars: "system".length + 1 + systemText(m).length, plan: 0 }
    default:
      throw new Error(`message role ${JSON.stringify(m.role)} is outside this core's workload`)
  }
}

/** The text pi-ai's faux provider hashes for a message (the oracle the index is tested against). */
export function messageText(m: any): string {
  switch (m.role) {
    case "system": return systemText(m)
    case "user": return typeof m.content === "string" ? m.content : m.content.map(blockText).join("\n")
    case "assistant": return m.content.map((b: Block) => b.type === "text" ? b.text : b.type === "thinking" ? b.thinking : `${b.name}:${JSON.stringify(b.arguments)}`).join("\n")
    default: return [m.toolName, ...m.content.map(blockText)].join("\n")
  }
}
