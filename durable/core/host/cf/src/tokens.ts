// The token estimates of a context read (ports packages/ai/src/utils/estimate.ts and the getSystemMessageText of utils/text.ts at the
// contract's reference commit, durable/contract/PIN main da866ada, as history/tokens.go does: 3.5 characters per token, as release
// 1.3.0 and the host's installed pi-ai count; release 1.0.4 counted 4). Lengths are JavaScript string lengths.
import type { Message } from "@earendil-works/pi-ai"

const CHARS_PER_TOKEN = 3.5
const ESTIMATED_IMAGE_CHARS = 4800

type Block = { type: string; text?: string; thinking?: string; name?: string; arguments?: unknown }
type Usage = { totalTokens: number; input: number; output: number; cacheRead: number; cacheWrite: number }

export function calculateContextTokens(usage: Usage): number {
  return usage.totalTokens || usage.input + usage.output + usage.cacheRead + usage.cacheWrite
}

function safeJsonStringify(value: unknown): string {
  try {
    return JSON.stringify(value) ?? "undefined"
  } catch {
    return "[unserializable]"
  }
}

function contentChars(content: string | readonly Block[]): number {
  if (typeof content === "string") return content.length
  let chars = 0
  for (const block of content) chars += block.type === "text" ? block.text!.length : ESTIMATED_IMAGE_CHARS
  return chars
}

function contentText(content: string | readonly Block[]): string {
  if (typeof content === "string") return content
  return content.filter(block => block.type === "text").map(block => block.text).join("\n")
}

function systemMessageText(message: { content: string | readonly Block[]; sections?: Record<string, string | null> }): string {
  const parts = [contentText(message.content)]
  for (const text of Object.values(message.sections ?? {})) if (text !== null) parts.push(text)
  return parts.filter(part => part.length > 0).join("\n\n")
}

const estimateTextTokens = (text: string) => Math.ceil(text.length / CHARS_PER_TOKEN)

function estimateToolsTokens(tools: readonly unknown[] | undefined): number {
  if (!tools || tools.length === 0) return 0
  return estimateTextTokens(safeJsonStringify(tools))
}

export function estimateMessageTokens(message: Message): number {
  const m = message as unknown as { role: string; content: string | readonly Block[]; sections?: Record<string, string | null>; toolsAdded?: unknown[]; toolsRemoved?: unknown[] }
  if (m.role === "system") return estimateTextTokens(systemMessageText(m)) + estimateToolsTokens(m.toolsAdded) + estimateToolsTokens(m.toolsRemoved)
  if (m.role === "user" || m.role === "toolResult") return Math.ceil(contentChars(m.content) / CHARS_PER_TOKEN)
  let chars = 0
  for (const block of m.content as readonly Block[]) {
    if (block.type === "text") chars += block.text!.length
    else if (block.type === "thinking") chars += block.thinking!.length
    else chars += block.name!.length + safeJsonStringify(block.arguments).length
  }
  return Math.ceil(chars / CHARS_PER_TOKEN)
}
