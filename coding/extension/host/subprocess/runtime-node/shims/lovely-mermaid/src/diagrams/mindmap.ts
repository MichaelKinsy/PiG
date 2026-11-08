/**
 * `mindmap`: an indentation tree, drawn the way every TUI draws trees
 * (`├──`/`└──` guides). Parses raw lines rather than statements — the
 * indentation IS the grammar, and `statementsOf` trims it away.
 */

import { Canvas, drawText } from '../canvas.ts'
import { MAX_NODES } from '../graph.ts'
import { cleanLabel, fitLabel, srcLines, WRAP_WIDTH } from '../labels.ts'
import type { Diagram } from '../registry.ts'
import { frontmatterEnd } from '../statements.ts'
import { stringWidth } from '../width.ts'

interface MindNode {
  text: string
  children: MindNode[]
}

export const mindmap: Diagram = {
  kind: 'mindmap',
  headers: ['mindmap'],
  render(src) {
    const parsed = parseMindmap(src)
    if (parsed === null) return null
    const { roots, warnings } = parsed

    /** [prefix, text] per row; prefixes carry the `│ ├ └` guides. */
    const rows: [string, string][] = []
    const walk = (node: MindNode, prefix: string, childPrefix: string): void => {
      rows.push([prefix, node.text])
      node.children.forEach((child, i) => {
        const last = i === node.children.length - 1
        walk(child, childPrefix + (last ? '└── ' : '├── '), childPrefix + (last ? '    ' : '│   '))
      })
    }
    for (const root of roots) walk(root, '', '')

    const width = Math.max(...rows.map(([p, t]) => stringWidth(p) + stringWidth(t)))
    const canvas = new Canvas(width, rows.length)
    rows.forEach(([prefix, text], y) => {
      drawText(canvas, prefix, 0, y, 'edge')
      drawText(canvas, text, stringWidth(prefix), y, 'text')
    })
    return { canvas, warnings, classDefs: {} }
  },
}

function parseMindmap(src: string): { roots: MindNode[]; warnings: string[] } | null {
  const lines = srcLines(src).slice(frontmatterEnd(srcLines(src)))
  const headerAt = lines.findIndex((l) => l.trim() !== '')
  if (headerAt === -1 || lines[headerAt].trim().toLowerCase() !== 'mindmap') return null

  const roots: MindNode[] = []
  const warnings: string[] = []
  /** Ancestors of the next node: the node at each indent level seen so far. */
  const stack: { indent: number; node: MindNode }[] = []
  let count = 0
  let truncated = false

  for (const raw of lines.slice(headerAt + 1)) {
    const noComment = raw.split('%%')[0]
    if (noComment.trim() === '') continue
    const indent = noComment.length - noComment.trimStart().length
    const body = noComment.trim()
    // Decoration lines attach to the previous node and draw nothing.
    if (body.startsWith('::icon') || body.startsWith(':::')) continue
    const text = nodeText(body)
    if (text === '') {
      warnings.push(`dropped, unreadable statement: "${body}"`)
      continue
    }
    if (count >= MAX_NODES) {
      truncated = true
      break
    }
    count++
    const node: MindNode = { text: fitLabel(text, WRAP_WIDTH), children: [] }
    while (stack.length > 0 && stack[stack.length - 1].indent >= indent) stack.pop()
    if (stack.length === 0) roots.push(node)
    else stack[stack.length - 1].node.children.push(node)
    stack.push({ indent, node })
  }
  if (truncated) warnings.push(`diagram truncated: node cap (${MAX_NODES}) reached`)

  return roots.length === 0 ? null : { roots, warnings }
}

/** Shape brackets around a mindmap node all mean "text" in a terminal. */
const SHAPES: [string, string][] = [
  ['((', '))'],
  ['))', '(('],
  ['(-', '-)'],
  ['{{', '}}'],
  ['[', ']'],
  ['(', ')'],
]

function nodeText(body: string): string {
  // `id((text))` — an optional id may precede the bracket.
  for (const [open, close] of SHAPES) {
    const at = body.indexOf(open)
    if (at !== -1 && body.endsWith(close) && body.length > at + open.length + close.length - 1) {
      return cleanLabel(body.slice(at + open.length, body.length - close.length))
    }
  }
  return cleanLabel(body)
}
