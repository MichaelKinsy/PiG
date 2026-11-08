/**
 * `graph` / `flowchart`: node chains with inline shapes and labelled links,
 * plus `subgraph` grouping.
 *
 * The grammar is lenient, inherited from upstream and mermaid.js itself:
 * a statement contributes whatever prefix parsed and the rest is dropped,
 * recorded in `graph.warnings`.
 */

import {
  Graph,
  type Head,
  type LineKind,
  MAX_GROUP_DEPTH,
  MAX_GROUPS,
  parseDir,
  type Shape,
} from '../graph.ts'
import { asciiLower, cleanLabel, decodeHtmlEntities, isIdChar } from '../labels.ts'
import { layoutFlowchart, layoutGrouped } from '../layout.ts'
import type { Diagram } from '../registry.ts'
import {
  firstWord,
  headerKind,
  nonEmpty,
  parseClassAssign,
  parseClassDef,
  parseHref,
  splitOnce,
  splitTop,
  statementsOf,
  words,
} from '../statements.ts'

export const flowchart: Diagram = {
  kind: 'flowchart',
  headers: ['graph', 'flowchart'],
  render(src) {
    const graph = parseGraph(src)
    if (graph === null) return null
    const canvas = graph.groups.length === 0 ? layoutFlowchart(graph) : layoutGrouped(graph)
    if (canvas === null) return null
    return { canvas, warnings: graph.warnings, classDefs: graph.classDefs }
  },
}

export function parseGraph(src: string): Graph | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !flowchart.headers.includes(kind)) return null

  const graph = new Graph(parseDir(words(statements[0])[1] ?? 'TB'))
  const stack: number[] = []
  /** `class A,B name` assignments, applied after the walk so a statement may
   * precede the nodes it names. Unknown ids are ignored. */
  const classAssignments: [string[], string[]][] = []
  /** `click A "url"` link targets, applied after the walk like classes. */
  const hrefs: [string, string][] = []

  for (const st of statements.slice(1)) {
    switch (asciiLower(firstWord(st))) {
      case 'subgraph': {
        if (graph.groups.length >= MAX_GROUPS || stack.length >= MAX_GROUP_DEPTH) {
          graph.truncated ??= `subgraph cap (${MAX_GROUPS} groups, depth ${MAX_GROUP_DEPTH}) reached`
          break
        }
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
      case 'classdef': {
        const def = parseClassDef(st.slice(firstWord(st).length))
        if (def) for (const name of def.names) graph.classDefs[name] = def.props
        continue
      }
      case 'class': {
        const assign = parseClassAssign(st.slice(firstWord(st).length))
        if (assign) classAssignments.push(assign)
        continue
      }
      case 'click': {
        // `click A "url" [tooltip]` / `click A href "url" …`; the callback
        // forms carry nothing a terminal can invoke.
        const target = parseHref(st.slice(firstWord(st).length))
        if (target) hrefs.push(target)
        continue
      }
      case 'style':
      case 'linkstyle':
      case 'direction':
        continue
      default:
        break
    }
    if (graph.truncated === null) parseStatement(st, graph)
    if (graph.truncated !== null) break
  }

  graph.applyClasses(classAssignments)
  graph.applyHrefs(hrefs)

  if (graph.truncated !== null) graph.warnings.push(`diagram truncated: ${graph.truncated}`)
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

  // A parse failure after the cap hit is the cap's fault, not the
  // statement's: the truncation warning already covers it (Graph.drop has
  // the same rule).
  const head = parseNodeGroup(chars, i, graph)
  if (!head) {
    if (graph.truncated === null)
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
      if (graph.truncated === null)
        graph.warnings.push(`dropped, expected a link: "${chars.slice(i).join('')}"`)
      break
    }
    i = skipSpaces(chars, link.next)
    const target = parseNodeGroup(chars, i, graph)
    if (!target) {
      if (graph.truncated === null) graph.warnings.push(`dropped, link has no target: "${st}"`)
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
  // `-` joins the id only when an id char follows, so kebab-case ids parse
  // while `-->` / `-.` / `--` still terminate (mermaid lexes ids greedily).
  while (
    i < chars.length &&
    (isIdChar(chars[i]) || (chars[i] === '-' && i + 1 < chars.length && isIdChar(chars[i + 1])))
  )
    i++
  if (i === idStart) return null
  const id = chars.slice(idStart, i).join('')

  const shaped =
    chars[i] === '@' && chars[i + 1] === '{' ? readAtShape(chars, i + 2) : readShapeAt(chars, i)
  if (shaped.unclosed !== undefined) {
    graph.warnings.push(`node "${id}": label is missing its closing \`${shaped.unclosed}\``)
  }
  const index = graph.nodeIndex(id, shaped.label, shaped.shape)
  if (index === null) return null

  // `id:::name` (after any shape) attaches an author class to the node —
  // upstream drops the rest of the line here.
  let next = shaped.after
  if (chars[next] === ':' && chars[next + 1] === ':' && chars[next + 2] === ':') {
    let k = next + 3
    while (k < chars.length && (isIdChar(chars[k]) || chars[k] === '-')) k++
    // A name never ends in `-`: back off so `A:::x-->B` keeps its link.
    while (k > next + 3 && chars[k - 1] === '-') k--
    if (k > next + 3) {
      graph.addClass(index, chars.slice(next + 3, k).join(''))
      next = k
    }
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

/**
 * Flowchart v2 shape names that read as something other than a plain box.
 * The terminal has three silhouettes; every name not listed here means
 * "some kind of box" and maps to `rect`.
 */
const AT_SHAPES: Record<string, Shape> = {
  rounded: 'round',
  stadium: 'round',
  pill: 'round',
  terminal: 'round',
  cyl: 'round',
  cylinder: 'round',
  database: 'round',
  db: 'round',
  circle: 'round',
  circ: 'round',
  'sm-circ': 'round',
  'small-circle': 'round',
  'dbl-circ': 'round',
  'double-circle': 'round',
  'fr-circ': 'round',
  'framed-circle': 'round',
  start: 'round',
  stop: 'round',
  event: 'round',
  delay: 'round',
  cloud: 'round',
  bang: 'round',
  diam: 'diamond',
  diamond: 'diamond',
  decision: 'diamond',
  question: 'diamond',
  hex: 'diamond',
  hexagon: 'diamond',
  prepare: 'diamond',
}

/**
 * The v2 node syntax `id@{shape: cyl, label: "..."}`, cursor past the `@{`.
 *
 * The body is `key: value` pairs split on top-level commas; quoted values may
 * contain commas and `}`. Unknown keys are ignored, unknown shapes draw as a
 * plain box. A body that never closes reports itself like any unterminated
 * label bracket.
 */
function readAtShape(chars: string[], start: number): Shaped {
  let i = start
  let depth = 0
  let inQuotes = false
  for (; i < chars.length; i++) {
    const c = chars[i]
    if (inQuotes) {
      if (c === '"') inQuotes = false
    } else if (c === '"') {
      inQuotes = true
    } else if (c === '{') {
      depth++
    } else if (c === '}') {
      if (depth === 0) break
      depth--
    }
  }
  const body = chars.slice(start, i).join('')
  const closed = chars[i] === '}'

  let shape: Shape = 'rect'
  let label: string | null = null
  for (const pair of splitTop(body, (c) => c === ',')) {
    const kv = splitOnce(pair, ':')
    if (kv === null) continue
    const key = asciiLower(kv[0].trim())
    if (key === 'shape') shape = AT_SHAPES[asciiLower(kv[1].trim())] ?? 'rect'
    else if (key === 'label') label = nonEmpty(cleanLabel(kv[1]))
  }
  return closed
    ? { shape, label, after: i + 1 }
    : { shape, label, after: chars.length, unclosed: '}' }
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
    // A quoted stretch keeps its `|`s as text: `-->|"a|b"|`.
    let inQuotes = false
    while (i < chars.length && (inQuotes || chars[i] !== '|')) {
      if (chars[i] === '"') inQuotes = !inQuotes
      i++
    }
    const label = cleanLabel(chars.slice(lStart, i).join(''))
    if (chars[i] === '|') i++
    return { left, right, line, label: nonEmpty(label), next: i }
  }

  if (right === 'none') {
    const textStart = skipSpaces(chars, i)
    let j = textStart
    // The label runs to the closing operator, which always starts with two
    // link chars (`--`, `-.`, `==`): a lone `=`/`.`/`-` is label text, and a
    // quoted stretch is label text throughout (`A --"a=b"--> B`).
    while (j < chars.length) {
      if (chars[j] === '"') {
        j++
        while (j < chars.length && chars[j] !== '"') j++
        if (j < chars.length) j++
      } else if (isLinkChar(chars[j]) && isLinkChar(chars[j + 1])) break
      else j++
    }
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
