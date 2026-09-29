/**
 * The raw source in a framed box.
 *
 * What to show when `render` returns `null`, or returns art too wide for the
 * space at hand. Both are the caller's call, so this is theirs to invoke — and
 * theirs to caption, since only they know whether some other view of the
 * diagram exists to point the reader at.
 */

import { srcLines, stripControls } from './labels.ts'
import type { MermaidArt, Span } from './types.ts'
import { measured, stringWidth } from './width.ts'

const sat = (a: number, b: number): number => Math.max(0, a - b)

/**
 * Frame `src` in a titled box, hard-wrapping its lines to `maxWidth` columns.
 *
 * The result can still exceed `maxWidth`: the body wraps to
 * `max(8, maxWidth - 4)` and the ` mermaid: <kind> ` title is never truncated,
 * so a long first token sets a floor. Check `width` if it matters.
 */
export function sourceBox(src: string, maxWidth?: number): MermaidArt {
  src = stripControls(src)
  const header = src.split(/\s+/).filter((w) => w !== '')[0] ?? 'diagram'
  const title = ` mermaid: ${header} `
  const limit = maxWidth === undefined ? undefined : Math.max(8, sat(maxWidth, 4))

  const body = srcLines(src)
    .map((l) => l.replace(/\s+$/, ''))
    .reduce<{ started: boolean; lines: string[] }>(
      (acc, l) => {
        if (!acc.started && l === '') return acc
        acc.started = true
        acc.lines.push(...chunkLine(l, limit))
        return acc
      },
      { started: false, lines: [] },
    ).lines

  const contentW = Math.max(stringWidth(title), ...body.map(stringWidth), 0)
  const inner = contentW + 2

  const plain: string[] = []
  const styled: Span[][] = []

  const rule = '─'.repeat(sat(inner, stringWidth(title)))
  plain.push(`╭${title}${rule}╮`)
  styled.push([
    { text: '╭', cls: 'border' },
    { text: title, cls: 'title' },
    { text: `${rule}╮`, cls: 'border' },
  ])

  for (const line of body) {
    const pad = ' '.repeat(sat(contentW, stringWidth(line)))
    plain.push(`│ ${line}${pad} │`)
    styled.push([
      { text: '│ ', cls: 'border' },
      { text: line, cls: 'text' },
      { text: `${pad} │`, cls: 'border' },
    ])
  }

  const bottom = `╰${'─'.repeat(inner)}╯`
  plain.push(bottom)
  styled.push([{ text: bottom, cls: 'border' }])

  return { plain, styled, width: inner + 2, warnings: [] }
}

/** Hard-break a line at `limit` columns, never splitting a wide glyph. */
function chunkLine(line: string, limit: number | undefined): string[] {
  if (limit === undefined || stringWidth(line) <= limit) return [line]
  const out: string[] = []
  let cur = ''
  let curW = 0
  for (const [c, cw] of measured(line)) {
    if (curW + cw > limit && cur !== '') {
      out.push(cur)
      cur = ''
      curW = 0
    }
    cur += c
    curW += cw
  }
  if (cur !== '') out.push(cur)
  return out
}
