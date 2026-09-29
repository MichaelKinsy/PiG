/**
 * Source text to diagram model.
 *
 * Every `parseX` returns `null` when the source is not that kind of diagram,
 * or when it exceeds a cap — `render` tries each in turn and falls back to a
 * framed copy of the source when they all decline.
 */

import {
  type ClassInfo,
  emptyClassInfo,
  Graph,
  type Head,
  type LineKind,
  MAX_EDGES,
  MAX_GROUP_DEPTH,
  MAX_GROUPS,
  MAX_MEMBERS,
  MAX_NODES,
  parseDir,
  type Shape,
} from './graph.ts'
import { asciiLower, cleanLabel, decodeHtmlEntities, isIdChar, srcLines } from './labels.ts'

// ---------------------------------------------------------------- statements

function flushStatement(cur: string, out: string[]): string {
  const trimmed = cur.trim()
  if (trimmed !== '') out.push(trimmed)
  return ''
}

/**
 * Split one source line into statements on `;`, stopping at a `%%` comment.
 *
 * Quoted spans are opaque, so a label may contain `;` and `%%`.
 */
export function splitStatements(line: string, out: string[]): void {
  const chars = [...line]
  let cur = ''
  let inQuotes = false
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i]
    if (inQuotes) {
      if (c === '"') inQuotes = false
      cur += c
    } else if (c === '"') {
      inQuotes = true
      cur += c
    } else if (c === '%' && chars[i + 1] === '%') {
      break
    } else if (c === ';') {
      cur = flushStatement(cur, out)
    } else {
      cur += c
    }
  }
  flushStatement(cur, out)
}

/** All statements in a source block, in order. */
export function statementsOf(src: string): string[] {
  const out: string[] = []
  for (const line of srcLines(src)) splitStatements(line, out)
  return out
}

const firstWord = (s: string): string => s.split(/\s+/).filter((w) => w !== '')[0] ?? ''
const words = (s: string): string[] => s.split(/\s+/).filter((w) => w !== '')

/** Split on the first occurrence of `sep`, Rust's `split_once`. */
function splitOnce(s: string, sep: string): [string, string] | null {
  const i = s.indexOf(sep)
  return i === -1 ? null : [s.slice(0, i), s.slice(i + sep.length)]
}

const nonEmpty = (s: string): string | null => (s === '' ? null : s)

/** Diagram kind from the header statement, lowercased. */
function headerKind(statements: string[]): string | null {
  const header = statements[0]
  if (header === undefined) return null
  const kind = firstWord(header)
  return kind === '' ? null : asciiLower(kind)
}

/** A diagram type this renderer draws. */
export type DiagramKind = 'flowchart' | 'state' | 'class' | 'er' | 'sequence'

/**
 * The kind of diagram `src` declares, or `null` if its header names no type
 * this renderer draws.
 *
 * Reads the header only — it says nothing about whether the body parses. Pair
 * it with `render` to tell a source this renderer will never draw from one that
 * is merely malformed:
 *
 * ```ts
 * render(src) === null && diagramKind(src) !== null   // syntax error
 * ```
 *
 * Each branch mirrors the header test in the matching `parseX`, so the two
 * always agree on what they recognise.
 */
export function diagramKind(src: string): DiagramKind | null {
  const kind = headerKind(statementsOf(src))
  if (kind === null) return null
  if (kind === 'graph' || kind === 'flowchart') return 'flowchart'
  if (kind.startsWith('statediagram')) return 'state'
  if (kind.startsWith('classdiagram')) return 'class'
  if (kind === 'erdiagram') return 'er'
  if (kind === 'sequencediagram') return 'sequence'
  return null
}

// ----------------------------------------------------------------- flowchart

export function parseGraph(src: string): Graph | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind !== 'graph' && kind !== 'flowchart') return null

  const graph = new Graph(parseDir(words(statements[0])[1] ?? 'TB'))
  const stack: number[] = []

  for (const st of statements.slice(1)) {
    switch (asciiLower(firstWord(st))) {
      case 'subgraph': {
        if (graph.groups.length >= MAX_GROUPS || stack.length >= MAX_GROUP_DEPTH) return null
        const [id, label] = parseSubgraphDecl(st.slice('subgraph'.length).trim())
        graph.groups.push({ id, label, parent: stack.at(-1) ?? null })
        stack.push(graph.groups.length - 1)
        graph.curGroup = stack.at(-1) ?? null
        continue
      }
      case 'end':
        stack.pop()
        graph.curGroup = stack.at(-1) ?? null
        continue
      case 'classdef':
      case 'class':
      case 'style':
      case 'linkstyle':
      case 'click':
      case 'direction':
        continue
      default:
        break
    }
    parseStatement(st, graph)
    if (graph.overCap) return null
  }

  return graph.nodes.length === 0 ? null : graph
}

/** `subgraph id[Title]`, `subgraph "Title"`, or a bare title. */
function parseSubgraphDecl(rest: string): [string, string] {
  if (rest.startsWith('"')) {
    const close = rest.indexOf('"', 1)
    if (close !== -1) {
      const label = rest.slice(1, close)
      return [label, decodeHtmlEntities(label)]
    }
  }
  const open = rest.indexOf('[')
  if (open !== -1) {
    const id = rest.slice(0, open).trim()
    const label = cleanLabel(
      rest
        .slice(open + 1)
        .replace(/\]+$/, '')
        .trim(),
    )
    if (id !== '' && label !== '') return [id, label]
  }
  return [rest, rest]
}

/**
 * A chain of `node link node link node ...`, each link fanning out over `&`.
 *
 * Parses as far as it can and keeps the prefix, matching upstream and
 * mermaid.js. Whatever it could not read is recorded in `graph.warnings` rather
 * than failing the diagram — see the note on that field.
 */
function parseStatement(st: string, graph: Graph): void {
  const chars = [...st]
  let i = 0

  const head = parseNodeGroup(chars, i, graph)
  if (!head) {
    graph.warnings.push(`dropped, does not start with a node: "${st}"`)
    return
  }
  let prev = head.group
  i = head.next

  for (;;) {
    i = skipSpaces(chars, i)
    if (i >= chars.length) break
    const link = parseLink(chars, i)
    if (!link) {
      graph.warnings.push(`dropped, expected a link: "${chars.slice(i).join('')}"`)
      break
    }
    i = skipSpaces(chars, link.next)
    const target = parseNodeGroup(chars, i, graph)
    if (!target) {
      graph.warnings.push(`dropped, link has no target: "${st}"`)
      break
    }
    i = target.next
    for (const f of prev) {
      for (const t of target.group) {
        // `A <-- B` reads right-to-left: swap the endpoints so the arrow that
        // was written on the left becomes a normal forward head.
        const reversed = link.left === 'arrow' && link.right !== 'arrow'
        const pushed = graph.pushEdge({
          from: reversed ? t : f,
          to: reversed ? f : t,
          label: link.label,
          headTo: reversed ? 'arrow' : link.right,
          headFrom: reversed ? link.right : link.left,
          line: link.line,
        })
        if (!pushed) return
      }
    }
    prev = target.group
  }
}

/** One or more nodes joined by `&`, which fan out into a cross product. */
function parseNodeGroup(
  chars: string[],
  start: number,
  graph: Graph,
): { group: number[]; next: number } | null {
  const first = parseNode(chars, start, graph)
  if (!first) return null
  const group = [first.index]
  let i = first.next
  for (;;) {
    const j = skipSpaces(chars, i)
    if (chars[j] !== '&') break
    const next = parseNode(chars, j + 1, graph)
    if (!next) return null
    group.push(next.index)
    i = next.next
  }
  return { group, next: i }
}

function skipSpaces(chars: string[], i: number): number {
  while (i < chars.length && (chars[i] === ' ' || chars[i] === '\t')) i++
  return i
}

function parseNode(
  chars: string[],
  start: number,
  graph: Graph,
): { index: number; next: number } | null {
  let i = skipSpaces(chars, start)
  const idStart = i
  while (i < chars.length && isIdChar(chars[i])) i++
  if (i === idStart) return null
  const id = chars.slice(idStart, i).join('')

  const shaped = readShapeAt(chars, i)
  if (shaped.unclosed !== undefined) {
    graph.warnings.push(`node "${id}": label is missing its closing \`${shaped.unclosed}\``)
  }
  const index = graph.nodeIndex(id, shaped.label, shaped.shape)
  if (index === null) return null

  // `id:::name` (after any shape) attaches a style class; swallow it so the
  // statement keeps parsing — upstream drops the rest of the line here.
  let next = shaped.after
  if (chars[next] === ':' && chars[next + 1] === ':' && chars[next + 2] === ':') {
    let k = next + 3
    while (k < chars.length && (isIdChar(chars[k]) || chars[k] === '-')) k++
    // A name never ends in `-`: back off so `A:::x-->B` keeps its link.
    while (k > next + 3 && chars[k - 1] === '-') k--
    if (k > next + 3) next = k
  }
  return { index, next }
}

/** What a shape bracket yielded. `closer` is set when the bracket never closed. */
interface Shaped {
  shape: Shape
  label: string | null
  after: number
  /** The closing token that was expected but never found. */
  unclosed?: string
}

/** Dispatch on the bracket following an id to pick shape and closing token. */
function readShapeAt(chars: string[], i: number): Shaped {
  const c = chars[i]
  const n = chars[i + 1]
  if (c === '[') {
    if (n === '[') return readShape(chars, i + 2, ']]', 'rect')
    if (n === '(') return readShape(chars, i + 2, ')]', 'round')
    return readShape(chars, i + 1, ']', 'rect')
  }
  if (c === '(') {
    if (n === '(') return readShape(chars, i + 2, '))', 'round')
    if (n === '[') return readShape(chars, i + 2, '])', 'round')
    return readShape(chars, i + 1, ')', 'round')
  }
  if (c === '{') {
    if (n === '{') return readShape(chars, i + 2, '}}', 'diamond')
    return readShape(chars, i + 1, '}', 'diamond')
  }
  if (c === '>') return readShape(chars, i + 1, ']', 'rect')
  return { shape: 'rect', label: null, after: i }
}

/**
 * Read label text up to `closer`.
 *
 * Quoting is decided by the first non-space character: inside a quoted label
 * the closer is ignored until the quote closes, so `A["a] b"]` is one node.
 * An unquoted label ends at the first closer, so `A[5" pipe]` keeps its quote.
 */
function readShape(chars: string[], start: number, closer: string, shape: Shape): Shaped {
  let j = start
  while (chars[j] === ' ' || chars[j] === '\t') j++
  const quoted = chars[j] === '"'

  let i = start
  let text = ''
  let inQuotes = false
  while (i < chars.length) {
    const c = chars[i]
    if (quoted && c === '"') {
      inQuotes = !inQuotes
      text += c
      i++
      continue
    }
    if (!inQuotes && chars.slice(i, i + closer.length).join('') === closer) {
      return { shape, label: cleanLabel(text), after: i + closer.length }
    }
    text += c
    i++
  }
  // Ran off the end still looking for the closer: everything after the opening
  // bracket became label text, so any link operator in it was swallowed.
  return { shape, label: cleanLabel(text), after: chars.length, unclosed: closer }
}

const isLinkChar = (c: string): boolean =>
  c === '-' || c === '.' || c === '=' || c === '<' || c === '>'

interface Link {
  left: Head
  right: Head
  line: LineKind
  label: string | null
  next: number
}

/**
 * Read a link operator and its label.
 *
 * Labels come in two forms: `-->|text|` and the inline `-- text -->`, the
 * latter only when the first operator carried no head.
 */
function parseLink(chars: string[], start: number): Link | null {
  let i = skipSpaces(chars, start)
  let left: Head = 'none'
  // A leading `o`/`x` decorates the tail, but only directly before an operator.
  if (
    (chars[i] === 'o' || chars[i] === 'x') &&
    (chars[i + 1] === '-' || chars[i + 1] === '.' || chars[i + 1] === '=')
  ) {
    left = chars[i] === 'o' ? 'circle' : 'cross'
    i++
  }

  const opStart = i
  while (i < chars.length && isLinkChar(chars[i])) i++
  if (i === opStart) return null
  const op1 = chars.slice(opStart, i).join('')
  if (left === 'none' && op1.startsWith('<')) left = 'arrow'

  let line = lineKind(op1)
  let right: Head = op1.includes('>') ? 'arrow' : 'none'
  if (right === 'none') {
    const trailing = trailingHead(chars, i)
    if (trailing) {
      right = trailing.head
      i = trailing.next
    }
  }

  if (chars[i] === '|') {
    i++
    const lStart = i
    while (i < chars.length && chars[i] !== '|') i++
    const label = cleanLabel(chars.slice(lStart, i).join(''))
    if (chars[i] === '|') i++
    return { left, right, line, label: nonEmpty(label), next: i }
  }

  if (right === 'none') {
    const textStart = skipSpaces(chars, i)
    let j = textStart
    while (j < chars.length && !isLinkChar(chars[j])) j++
    if (j < chars.length && j > textStart && chars[j] !== '<') {
      const text = chars.slice(textStart, j).join('')
      const op2Start = j
      while (j < chars.length && isLinkChar(chars[j])) j++
      const op2 = chars.slice(op2Start, j).join('')
      if (op2.includes('>')) {
        right = 'arrow'
      } else {
        const trailing = trailingHead(chars, j)
        if (trailing) {
          right = trailing.head
          j = trailing.next
        }
      }
      if (line === 'solid') line = lineKind(op2)
      return { left, right, line, label: nonEmpty(cleanLabel(text)), next: j }
    }
  }

  return { left, right, line, label: null, next: i }
}

function lineKind(op: string): LineKind {
  if (op.includes('=')) return 'thick'
  if (op.includes('.')) return 'dotted'
  return 'solid'
}

/** A trailing `o`/`x` head, only when followed by a statement boundary. */
function trailingHead(chars: string[], i: number): { head: Head; next: number } | null {
  const head: Head | null = chars[i] === 'o' ? 'circle' : chars[i] === 'x' ? 'cross' : null
  if (head === null) return null
  const after = chars[i + 1]
  const boundary =
    after === undefined ||
    after === ' ' ||
    after === '\t' ||
    after === '|' ||
    after === '&' ||
    after === ';'
  return boundary ? { head, next: i + 1 } : null
}

// --------------------------------------------------------------------- state

export function parseState(src: string): Graph | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !kind.startsWith('statediagram')) return null

  const graph = new Graph()
  let inNote = false

  for (let st of statements.slice(1)) {
    if (inNote) {
      if (asciiLower(st) === 'end note') inNote = false
      continue
    }
    st = dropStyleTags(st)
    const first = asciiLower(firstWord(st))
    if (first === 'direction') {
      graph.dir = parseDir(words(st)[1] ?? '')
    } else if (first === 'note') {
      // A single-line `note ... : text` needs no terminator.
      if (!st.includes(':')) inNote = true
    } else if (first === 'state') {
      if (parseStateDecl(st, graph) === null) return null
    } else if (['classdef', 'class', 'hide', 'scale', '}', '--'].includes(first)) {
      // Styling and composite-state punctuation carry no layout meaning.
    } else if (st.includes('-->')) {
      if (parseTransition(st, graph) === null) return null
    } else if (parseStateDesc(st, graph) === null) {
      return null
    }
    if (graph.overCap) return null
  }

  return graph.nodes.length === 0 ? null : graph
}

/** `state "Label" as id`, `state id <<choice>>`, or `state id {`. */
function parseStateDecl(st: string, graph: Graph): true | null {
  const rest = st.slice('state'.length).trim().replace(/\{$/, '').trim()
  if (rest === '') return true

  if (rest.startsWith('"')) {
    const close = rest.indexOf('"', 1)
    if (close === -1) return null
    const label = rest.slice(1, close)
    const after = rest.slice(close + 1).trim()
    const id = after.startsWith('as') ? after.slice(2).trim() : label
    return graph.nodeLabel(id, decodeHtmlEntities(label)) === null ? null : true
  }

  let shape: Shape = 'round'
  let id = rest
  let stereotyped = false
  const pos = rest.indexOf('<<')
  if (pos !== -1) {
    const stereo = rest
      .slice(pos + 2)
      .replace(/>>$/, '')
      .trim()
    if (stereo === 'choice') shape = 'diamond'
    id = rest.slice(0, pos).trim()
    stereotyped = true
  }
  if (id === '' || /\s/.test(id)) return null
  return graph.nodeIndex(id, stereotyped ? id : null, shape) === null ? null : true
}

/** `A --> B: label`, including chains `A --> B --> C`. */
function parseTransition(st: string, graph: Graph): true | null {
  let rest = st
  let prev: number | null = null

  for (;;) {
    const split = splitOnce(rest, '-->')
    if (!split) break
    const [lhs, rhs] = split

    const fromId = lhs.trimEnd().replace(/-+$/, '').trim()
    let from: number
    if (prev !== null) {
      // Mid-chain: the source is the previous target, so nothing may precede.
      if (fromId !== '') return null
      from = prev
    } else {
      if (fromId === '') return null
      const f = stateEndpoint(graph, fromId, true)
      if (f === null) return null
      from = f
    }

    const nextArrow = rhs.indexOf('-->')
    const toPartRaw = nextArrow === -1 ? rhs : rhs.slice(0, nextArrow)
    const tail = nextArrow === -1 ? '' : rhs.slice(nextArrow)

    const colon = splitOnce(toPartRaw, ':')
    const toPart = colon ? colon[0] : toPartRaw
    const label = colon ? nonEmpty(decodeHtmlEntities(colon[1].trim())) : null

    const toId = toPart.trimStart().replace(/^>+/, '').trimEnd().replace(/-+$/, '').trim()
    if (toId === '') return null
    const to = stateEndpoint(graph, toId, false)
    if (to === null) return null

    if (!graph.pushEdge({ from, to, label, headTo: 'arrow', headFrom: 'none', line: 'solid' })) {
      return true
    }
    prev = to
    rest = tail
  }
  return true
}

/**
 * Remove every `:::name` style tag from a statement, parsed with the same
 * name scan as the flowchart shorthand. State and class statements are read
 * by string splits, and the `:` label split would cut inside a `:::`, so tags
 * are dropped before dispatch rather than at each id.
 */
function dropStyleTags(st: string): string {
  const chars = [...st]
  const out: string[] = []
  for (let i = 0; i < chars.length; ) {
    if (chars[i] === ':' && chars[i + 1] === ':' && chars[i + 2] === ':') {
      let k = i + 3
      while (k < chars.length && (isIdChar(chars[k]) || chars[k] === '-')) k++
      while (k > i + 3 && chars[k - 1] === '-') k--
      if (k > i + 3) {
        i = k
        continue
      }
    }
    out.push(chars[i])
    i++
  }
  return out.join('')
}

/** `[*]` is start or end depending on which side of the arrow it sits. */
function stateEndpoint(graph: Graph, id: string, isSource: boolean): number | null {
  if (id === '[*]') return graph.nodeIndex(isSource ? '[*]start' : '[*]end', '●', 'round')
  return graph.nodeIndex(id, null, 'round')
}

/** `id: description`, or a bare state name. */
function parseStateDesc(st: string, graph: Graph): true | null {
  const split = splitOnce(st, ':')
  if (split) {
    const id = split[0].trim()
    const desc = split[1].trim()
    if (id === '' || /\s/.test(id) || desc === '') return null
    return graph.nodeLabel(id, decodeHtmlEntities(desc)) === null ? null : true
  }
  if (/\s/.test(st)) return null
  return graph.nodeIndex(st, null, 'round') === null ? null : true
}

// --------------------------------------------------------------------- class

/** Relation operators, longest-first so `--|>` wins over `--`. */
const CLASS_OPS: [string, Head, Head, LineKind][] = [
  ['<|--', 'triangle', 'none', 'solid'],
  ['--|>', 'none', 'triangle', 'solid'],
  ['<|..', 'triangle', 'none', 'dotted'],
  ['..|>', 'none', 'triangle', 'dotted'],
  ['*--', 'diamondFill', 'none', 'solid'],
  ['--*', 'none', 'diamondFill', 'solid'],
  ['o--', 'diamondOpen', 'none', 'solid'],
  ['--o', 'none', 'diamondOpen', 'solid'],
  ['<--', 'arrow', 'none', 'solid'],
  ['-->', 'none', 'arrow', 'solid'],
  ['<..', 'arrow', 'none', 'dotted'],
  ['..>', 'none', 'arrow', 'dotted'],
  ['--', 'none', 'none', 'solid'],
  ['..', 'none', 'none', 'dotted'],
]

const MAX_CLASS_OP = 4

export function parseClass(src: string): { graph: Graph; infos: ClassInfo[] } | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !kind.startsWith('classdiagram')) return null

  const graph = new Graph()
  const infos: ClassInfo[] = []
  const sync = () => {
    while (infos.length < graph.nodes.length) infos.push(emptyClassInfo())
  }
  /** Declare a class, keeping `infos` aligned with `graph.nodes`. */
  const declare = (name: string): number | null => {
    const idx = graph.nodeIndex(name, null, 'rect')
    sync()
    return idx
  }
  let curClass: number | null = null

  for (let st of statements.slice(1)) {
    if (curClass !== null) {
      if (st === '}') curClass = null
      else pushMember(infos[curClass], st)
      continue
    }
    st = dropStyleTags(st)

    const first = asciiLower(firstWord(st))
    if (first === 'direction') {
      graph.dir = parseDir(words(st)[1] ?? '')
      continue
    }
    if (
      [
        'note',
        'callback',
        'click',
        'link',
        'style',
        'cssclass',
        'classdef',
        'namespace',
        '}',
      ].includes(first)
    ) {
      continue
    }
    if (first === 'class') {
      const rest = st.slice('class'.length).trim()
      const open = rest.endsWith('{')
      const name = open ? rest.slice(0, -1).trim() : rest
      if (name === '' || /\s/.test(name)) return null
      const idx = declare(name)
      if (idx === null) return null
      if (open) curClass = idx
      continue
    }

    if (st.startsWith('<<')) {
      const split = splitOnce(st.slice(2), '>>')
      if (!split) return null
      const name = split[1].trim()
      if (name === '' || /\s/.test(name)) return null
      const idx = declare(name)
      if (idx === null) return null
      infos[idx].annotation = split[0].trim()
      continue
    }

    const rel = parseClassRelation(st)
    if (rel) {
      const f = declare(rel.from)
      if (f === null) return null
      const t = declare(rel.to)
      if (t === null) return null
      if (graph.edges.length >= MAX_EDGES) return null
      graph.edges.push({
        from: f,
        to: t,
        label: rel.label,
        headTo: rel.headTo,
        headFrom: rel.headFrom,
        line: rel.line,
      })
      continue
    }

    const member = splitOnce(st, ':')
    if (member) {
      const id = member[0].trim()
      const text = member[1].trim()
      if (id === '' || /\s/.test(id) || text === '') return null
      const idx = declare(id)
      if (idx === null) return null
      pushMember(infos[idx], text)
      continue
    }
    return null
  }

  if (graph.nodes.length === 0) return null
  sync()
  return { graph, infos }
}

/** Add a member to the attribute or method compartment, eliding past the cap. */
export function pushMember(info: ClassInfo, raw: string): void {
  if (raw.startsWith('<<')) {
    const split = splitOnce(raw.slice(2), '>>')
    if (split) info.annotation = split[0].trim()
    return
  }
  const member = decodeHtmlEntities(displayGenerics(raw.trim()))
  const list = member.includes('(') ? info.methods : info.attrs
  if (list.length < MAX_MEMBERS) list.push(member)
  else if (list.length === MAX_MEMBERS) list.push('…')
}

interface ClassRelation {
  from: string
  to: string
  headFrom: Head
  headTo: Head
  line: LineKind
  label: string | null
}

function parseClassRelation(st: string): ClassRelation | null {
  const chars = [...st]
  let found: { pos: number; op: string; headFrom: Head; headTo: Head; line: LineKind } | null = null

  outer: for (let pos = 0; pos < chars.length; pos++) {
    const tail = chars.slice(pos, pos + MAX_CLASS_OP).join('')
    for (const [op, headFrom, headTo, line] of CLASS_OPS) {
      if (!tail.startsWith(op)) continue
      // `o` is also an identifier character: skip a match glued to a name.
      if (op.startsWith('o') && pos > 0 && isIdChar(chars[pos - 1])) continue
      const after = chars[pos + [...op].length]
      if (op.endsWith('o') && after !== undefined && isIdChar(after)) continue
      found = { pos, op, headFrom, headTo, line }
      break outer
    }
  }
  if (!found) return null

  const lhsRaw = chars.slice(0, found.pos).join('').trim()
  const rhsRaw = chars
    .slice(found.pos + [...found.op].length)
    .join('')
    .trim()

  const [lhs, cardFrom] = stripCardinalitySuffix(lhsRaw)
  const [rhs, cardTo] = stripCardinalityPrefix(rhsRaw)

  const split = splitOnce(rhs, ':')
  const toId = (split ? split[0] : rhs).trim()
  const relLabel = split ? nonEmpty(decodeHtmlEntities(split[1].trim())) : null

  if (lhs === '' || toId === '' || /\s/.test(lhs) || /\s/.test(toId)) return null

  const label = nonEmpty([cardFrom, relLabel ?? '', cardTo].filter((s) => s !== '').join(' '))
  return {
    from: lhs,
    to: toId,
    headFrom: found.headFrom,
    headTo: found.headTo,
    line: found.line,
    label,
  }
}

/** `Class "1"` — a quoted cardinality trailing the left-hand name. */
function stripCardinalitySuffix(s: string): [string, string] {
  const t = s.trimEnd()
  if (t.endsWith('"')) {
    const rest = t.slice(0, -1)
    const q = rest.lastIndexOf('"')
    if (q !== -1) return [rest.slice(0, q).trimEnd(), rest.slice(q + 1)]
  }
  return [t, '']
}

/** `"0..*" Class` — a quoted cardinality leading the right-hand name. */
function stripCardinalityPrefix(s: string): [string, string] {
  const t = s.trimStart()
  if (t.startsWith('"')) {
    const rest = t.slice(1)
    const q = rest.indexOf('"')
    if (q !== -1) return [rest.slice(q + 1).trimStart(), rest.slice(0, q)]
  }
  return [t, '']
}

/** Mermaid writes generics as `List~T~`; show them as `List<T>`. */
function displayGenerics(s: string): string {
  let out = ''
  let open = false
  for (const c of s) {
    if (c === '~') {
      out += open ? '>' : '<'
      open = !open
    } else {
      out += c
    }
  }
  return out
}

// ------------------------------------------------------------------------ ER

export function parseEr(src: string): { graph: Graph; infos: ClassInfo[] } | null {
  const statements = statementsOf(src)
  if (headerKind(statements) !== 'erdiagram') return null

  const graph = new Graph()
  const infos: ClassInfo[] = []
  let curEntity: number | null = null

  for (const st of statements.slice(1)) {
    if (curEntity !== null) {
      if (st === '}') curEntity = null
      else pushErAttribute(infos[curEntity], st)
      continue
    }

    const rel = splitErRelationship(st)
    if (rel) {
      const tokens = words(rel.rel)
      if (tokens.length !== 3) return null
      const op = parseErOp(tokens[1])
      if (!op) return null
      const f = erEntity(graph, infos, tokens[0])
      if (f === null) return null
      const t = erEntity(graph, infos, tokens[2])
      if (t === null) return null
      if (graph.edges.length >= MAX_EDGES) return null
      const relLabel = rel.label === null ? '' : cleanLabel(rel.label)
      graph.edges.push({
        from: f,
        to: t,
        label: nonEmpty([op.cardL, relLabel, op.cardR].filter((s) => s !== '').join(' ')),
        headTo: 'none',
        headFrom: 'none',
        line: op.line,
      })
      continue
    }

    const open = st.endsWith('{')
    const decl = open ? st.slice(0, -1).trim() : st
    if (decl === '' || words(decl).length !== 1) return null
    const idx = erEntity(graph, infos, decl)
    if (idx === null) return null
    if (open) curEntity = idx
  }

  if (graph.nodes.length === 0) return null
  while (infos.length < graph.nodes.length) infos.push(emptyClassInfo())
  return { graph, infos }
}

function erEntity(graph: Graph, infos: ClassInfo[], token: string): number | null {
  const open = token.indexOf('[')
  let idx: number | null
  if (open !== -1) {
    const id = token.slice(0, open)
    const label = cleanLabel(token.slice(open + 1).replace(/\]+$/, ''))
    if (id === '' || label === '') return null
    idx = graph.nodeLabel(id, label)
  } else {
    idx = graph.nodeIndex(token, null, 'rect')
  }
  if (idx === null) return null
  while (infos.length < graph.nodes.length) infos.push(emptyClassInfo())
  return idx
}

function splitErRelationship(st: string): { rel: string; label: string | null } | null {
  const split = splitOnce(st, ':')
  const rel = split ? split[0] : st
  const label = split ? split[1].trim() : null
  return words(rel).some((t) => parseErOp(t) !== null) ? { rel, label } : null
}

const isAscii = (s: string): boolean => {
  for (let i = 0; i < s.length; i++) if (s.charCodeAt(i) > 0x7f) return false
  return true
}

/** A crow's-foot operator: two cardinality glyphs around `--` or `..`. */
function parseErOp(tok: string): { cardL: string; cardR: string; line: LineKind } | null {
  if (tok.length !== 6 || !isAscii(tok)) return null
  const mid = tok.slice(2, 4)
  const line: LineKind | null = mid === '--' ? 'solid' : mid === '..' ? 'dotted' : null
  if (line === null) return null
  const cardL = erCard(tok.slice(0, 2))
  const cardR = erCard(tok.slice(4, 6))
  return cardL === null || cardR === null ? null : { cardL, cardR, line }
}

function erCard(tok: string): string | null {
  switch (tok) {
    case '|o':
    case 'o|':
      return '0..1'
    case '||':
      return '1'
    case '}o':
    case 'o{':
      return '0..*'
    case '}|':
    case '|{':
      return '1..*'
    default:
      return null
  }
}

/** ER attributes are `type name`; a trailing quoted comment is dropped. */
export function pushErAttribute(info: ClassInfo, raw: string): void {
  const parts: string[] = []
  for (const tok of words(raw)) {
    if (tok.startsWith('"')) break
    parts.push(tok)
  }
  if (parts.length === 0) return
  const line = decodeHtmlEntities(parts.join(' '))
  if (info.attrs.length < MAX_MEMBERS) info.attrs.push(line)
  else if (info.attrs.length === MAX_MEMBERS) info.attrs.push('…')
}

// ------------------------------------------------------------------ sequence

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

export class Sequence {
  labels: string[] = []
  index = new Map<string, number>()
  items: SeqItem[] = []

  participant(id: string, label: string | null): number | null {
    const existing = this.index.get(id)
    if (existing !== undefined) {
      if (label !== null) this.labels[existing] = label
      return existing
    }
    if (this.labels.length >= MAX_NODES) return null
    this.index.set(id, this.labels.length)
    this.labels.push(label ?? id)
    return this.labels.length - 1
  }
}

export function parseSequence(src: string): Sequence | null {
  const statements = statementsOf(src)
  if (headerKind(statements) !== 'sequencediagram') return null

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
      if (rest === '') return null
      const as = splitOnce(rest, ' as ')
      if (seq.participant(as ? as[0].trim() : rest, as ? cleanLabel(as[1]) : null) === null) {
        return null
      }
      continue
    }
    if (lower === 'autonumber') {
      autonumber = true
      continue
    }
    if (
      [
        'activate',
        'deactivate',
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
      continue
    }
    if (lower === 'note') {
      const note = parseNoteAnchor(st.slice(first.length).trim(), seq)
      if (!note) return null
      if (seq.items.length >= MAX_EDGES) return null
      seq.items.push({ kind: 'note', anchor: note.anchor, text: note.text })
      continue
    }
    if (
      ['loop', 'alt', 'opt', 'par', 'critical', 'break', 'else', 'and', 'option'].includes(lower)
    ) {
      if (['else', 'and', 'option'].includes(lower)) {
        // A continuation only divides a block that opened one.
        if (blocks.at(-1) !== true) continue
      } else {
        blocks.push(true)
      }
      if (seq.items.length >= MAX_EDGES) return null
      seq.items.push({ kind: 'divider', text: decodeHtmlEntities(st) })
      continue
    }
    if (lower === 'rect' || lower === 'box') {
      blocks.push(false)
      continue
    }
    if (lower === 'end') {
      if (blocks.pop() === true) {
        if (seq.items.length >= MAX_EDGES) return null
        seq.items.push({ kind: 'divider', text: 'end' })
      }
      continue
    }

    const msg = parseSeqMessage(st, seq)
    if (!msg) return null
    let text = msg.text
    if (autonumber) {
      msgCount++
      text = text === null ? `${msgCount}.` : `${msgCount}. ${text}`
    }
    if (seq.items.length >= MAX_EDGES) return null
    seq.items.push({
      kind: 'message',
      from: msg.from,
      to: msg.to,
      text,
      dashed: msg.dashed,
      head: msg.head,
    })
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
): { from: number; to: number; text: string | null; dashed: boolean; head: SeqHead } | null {
  const chars = [...st]
  let found: { pos: number; op: string; dashed: boolean; head: SeqHead } | null = null
  outer: for (let pos = 0; pos < chars.length; pos++) {
    const tail = chars.slice(pos, pos + MAX_SEQ_OP).join('')
    for (const [op, dashed, head] of SEQ_OPS) {
      if (tail.startsWith(op)) {
        found = { pos, op, dashed, head }
        break outer
      }
    }
  }
  if (!found) return null

  const fromId = chars.slice(0, found.pos).join('').trim()
  if (fromId === '') return null
  // `+`/`-` activate and deactivate the target; they carry no layout meaning.
  const rest = chars
    .slice(found.pos + [...found.op].length)
    .join('')
    .trimStart()
    .replace(/^[+-]+/, '')

  const split = splitOnce(rest, ':')
  const toId = (split ? split[0] : rest).trim()
  const text = split ? nonEmpty(decodeHtmlEntities(split[1].trim())) : null
  if (toId === '') return null

  const from = seq.participant(fromId, null)
  if (from === null) return null
  const to = seq.participant(toId, null)
  if (to === null) return null
  return { from, to, text, dashed: found.dashed, head: found.head }
}
