/**
 * `sequenceDiagram`: participants, messages, notes and block dividers.
 * Lenient: an unreadable statement is dropped and recorded in `warnings`.
 *
 * Sequence diagrams have their own model — participants in declaration order
 * plus a flat list of items — and their own geometry in `layout-seq.ts`.
 */

import { MAX_EDGES, MAX_NODES } from '../graph.ts'
import { asciiLower, cleanLabel, decodeHtmlEntities } from '../labels.ts'
import { layoutSequence } from '../layout-seq.ts'
import type { Diagram } from '../registry.ts'
import { firstWord, headerKind, nonEmpty, splitOnce, statementsOf } from '../statements.ts'

export const sequence: Diagram = {
  kind: 'sequence',
  headers: ['sequencediagram'],
  render(src) {
    const seq = parseSequence(src)
    if (seq === null) return null
    const canvas = layoutSequence(seq)
    if (canvas === null) return null
    return { canvas, warnings: seq.warnings, classDefs: {} }
  },
}

export type SeqHead = 'arrow' | 'cross'

/** Message operators, longest-first so `-->>` wins over `-->`. */
const SEQ_OPS: [string, boolean, SeqHead][] = [
  ['-->>', true, 'arrow'],
  ['->>', false, 'arrow'],
  ['--x', true, 'cross'],
  ['-x', false, 'cross'],
  ['--)', true, 'arrow'],
  ['-)', false, 'arrow'],
  ['-->', true, 'arrow'],
  ['->', false, 'arrow'],
]

const MAX_SEQ_OP = 4

export type NoteAnchor =
  | { kind: 'over'; from: number; to: number }
  | { kind: 'left'; at: number }
  | { kind: 'right'; at: number }

export type SeqItem =
  | {
      kind: 'message'
      from: number
      to: number
      text: string | null
      dashed: boolean
      head: SeqHead
    }
  | { kind: 'note'; anchor: NoteAnchor; text: string }
  | { kind: 'divider'; text: string }

/** A hot span of one lifeline: item indices, `to === null` while still open. */
export interface Activation {
  at: number
  from: number
  to: number | null
}

export class Sequence {
  labels: string[] = []
  index = new Map<string, number>()
  items: SeqItem[] = []
  activations: Activation[] = []
  /** Set when a size cap was hit; the parser stops and renders the prefix. */
  truncated: string | null = null
  /** Statements the grammar could not read and dropped. */
  warnings: string[] = []

  participant(id: string, label: string | null): number | null {
    const existing = this.index.get(id)
    if (existing !== undefined) {
      if (label !== null) this.labels[existing] = label
      return existing
    }
    if (this.labels.length >= MAX_NODES) {
      this.truncated ??= `participant cap (${MAX_NODES}) reached`
      return null
    }
    this.index.set(id, this.labels.length)
    this.labels.push(label ?? id)
    return this.labels.length - 1
  }

  /** Append an item, or flag `truncated` when the item cap is reached. */
  pushItem(item: SeqItem): void {
    if (this.items.length >= MAX_EDGES) this.truncated ??= `item cap (${MAX_EDGES}) reached`
    else this.items.push(item)
  }

  /** Record an unreadable statement; skipped once truncated. */
  drop(st: string): void {
    if (this.truncated === null) this.warnings.push(`dropped, unreadable statement: "${st}"`)
  }

  /** Open an activation at item `from`. */
  activate(at: number, from: number): void {
    this.activations.push({ at, from, to: null })
  }

  /** Close the innermost open activation of `at` at item `to`. */
  deactivate(at: number, to: number): void {
    for (let i = this.activations.length - 1; i >= 0; i--) {
      const a = this.activations[i]
      if (a.at === at && a.to === null) {
        a.to = to
        return
      }
    }
  }
}

export function parseSequence(src: string): Sequence | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !sequence.headers.includes(kind)) return null

  const seq = new Sequence()
  let autonumber = false
  let msgCount = 0
  /** One entry per open block; `true` when it draws a divider on `end`. */
  const blocks: boolean[] = []

  for (const st of statements.slice(1)) {
    const first = firstWord(st)
    const lower = asciiLower(first)

    if (lower === 'participant' || lower === 'actor') {
      const rest = st.slice(first.length).trim()
      if (rest === '') {
        seq.drop(st)
      } else {
        const as = splitOnce(rest, ' as ')
        seq.participant(as ? as[0].trim() : rest, as ? cleanLabel(as[1]) : null)
      }
    } else if (lower === 'autonumber') {
      autonumber = true
    } else if (lower === 'activate' || lower === 'deactivate') {
      // Applies to the preceding message's row.
      const who = seq.index.get(st.slice(first.length).trim())
      if (who !== undefined && seq.items.length > 0) {
        if (lower === 'activate') seq.activate(who, seq.items.length - 1)
        else seq.deactivate(who, seq.items.length - 1)
      }
    } else if (
      [
        'create',
        'destroy',
        'title',
        'acctitle',
        'accdescr',
        'links',
        'link',
        'properties',
      ].includes(lower)
    ) {
      // No layout meaning.
    } else if (lower === 'note') {
      const note = parseNoteAnchor(st.slice(first.length).trim(), seq)
      if (!note) seq.drop(st)
      else seq.pushItem({ kind: 'note', anchor: note.anchor, text: note.text })
    } else if (
      ['loop', 'alt', 'opt', 'par', 'critical', 'break', 'else', 'and', 'option'].includes(lower)
    ) {
      // A continuation only divides a block that opened one.
      const continues = ['else', 'and', 'option'].includes(lower)
      if (!continues) blocks.push(true)
      if (!continues || blocks.at(-1) === true) {
        seq.pushItem({ kind: 'divider', text: decodeHtmlEntities(st) })
      }
    } else if (lower === 'rect' || lower === 'box') {
      blocks.push(false)
    } else if (lower === 'end') {
      if (blocks.pop() === true) seq.pushItem({ kind: 'divider', text: 'end' })
    } else {
      const msg = parseSeqMessage(st, seq)
      if (!msg) {
        seq.drop(st)
      } else {
        let text = msg.text
        if (autonumber) {
          msgCount++
          text = text === null ? `${msgCount}.` : `${msgCount}. ${text}`
        }
        const item = seq.items.length
        seq.pushItem({
          kind: 'message',
          from: msg.from,
          to: msg.to,
          text,
          dashed: msg.dashed,
          head: msg.head,
        })
        // `+` activates the receiver on this row; `-` deactivates the sender.
        // Only once the item was accepted — an activation pointing past the
        // item cap would dereference a message that does not exist.
        if (seq.items.length === item + 1) {
          if (msg.marks.includes('+')) seq.activate(msg.to, item)
          if (msg.marks.includes('-')) seq.deactivate(msg.from, item)
        }
      }
    }
    if (seq.truncated !== null) {
      seq.warnings.push(`diagram truncated: ${seq.truncated}`)
      break
    }
  }

  return seq.labels.length === 0 ? null : seq
}

function parseNoteAnchor(rest: string, seq: Sequence): { text: string; anchor: NoteAnchor } | null {
  const lower = asciiLower(rest)
  let kind: NoteAnchor['kind']
  let idsAndText: string
  if (lower.startsWith('over ')) {
    kind = 'over'
    idsAndText = rest.slice('over '.length)
  } else if (lower.startsWith('left of ')) {
    kind = 'left'
    idsAndText = rest.slice('left of '.length)
  } else if (lower.startsWith('right of ')) {
    kind = 'right'
    idsAndText = rest.slice('right of '.length)
  } else {
    return null
  }

  const split = splitOnce(idsAndText, ':')
  if (!split) return null
  const text = decodeHtmlEntities(split[1].trim())
  const parts = split[0]
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '')
  if (parts.length === 0) return null
  const a = seq.participant(parts[0], null)
  if (a === null) return null

  if (kind !== 'over') return { text, anchor: { kind, at: a } }
  let b = a
  if (parts[1] !== undefined) {
    const second = seq.participant(parts[1], null)
    if (second === null) return null
    b = second
  }
  return { text, anchor: { kind: 'over', from: Math.min(a, b), to: Math.max(a, b) } }
}

function parseSeqMessage(
  st: string,
  seq: Sequence,
): {
  from: number
  to: number
  text: string | null
  dashed: boolean
  head: SeqHead
  marks: string
} | null {
  const chars = [...st]
  let found: { pos: number; op: string; dashed: boolean; head: SeqHead } | null = null
  outer: for (let pos = 0; pos < chars.length; pos++) {
    const tail = chars.slice(pos, pos + MAX_SEQ_OP).join('')
    for (const [op, dashed, head] of SEQ_OPS) {
      if (tail.startsWith(op)) {
        // `-x` / `-)` embedded in a hyphenated token (`pre-x->>B`) is not an
        // operator — the real one follows. Require a non-link char after.
        const after = chars[pos + op.length]
        if ((op.endsWith('x') || op.endsWith(')')) && (after === '-' || after === '>')) continue
        found = { pos, op, dashed, head }
        break outer
      }
    }
  }
  if (!found) return null

  const fromId = chars.slice(0, found.pos).join('').trim()
  if (fromId === '') return null
  // `+` activates the receiver, `-` deactivates the sender.
  const afterOp = chars
    .slice(found.pos + [...found.op].length)
    .join('')
    .trimStart()
  const marks = afterOp.match(/^[+-]+/)?.[0] ?? ''
  const rest = afterOp.slice(marks.length)

  const split = splitOnce(rest, ':')
  const toId = (split ? split[0] : rest).trim()
  const text = split ? nonEmpty(decodeHtmlEntities(split[1].trim())) : null
  if (toId === '') return null

  const from = seq.participant(fromId, null)
  if (from === null) return null
  const to = seq.participant(toId, null)
  if (to === null) return null
  return { from, to, text, dashed: found.dashed, head: found.head, marks }
}
