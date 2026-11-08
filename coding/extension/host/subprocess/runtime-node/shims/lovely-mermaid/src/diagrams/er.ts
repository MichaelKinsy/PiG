/**
 * `erDiagram`: entities with attribute compartments and crow's-foot
 * relationships. Lenient: an unreadable statement is dropped and recorded in
 * `warnings`.
 *
 * Entities use `Node.sections` as `[title, attrs]`.
 */

import { Graph, type LineKind, MAX_MEMBERS, type Node } from '../graph.ts'
import { cleanLabel, decodeHtmlEntities, displayGenerics } from '../labels.ts'
import { layoutClass } from '../layout.ts'
import type { Diagram } from '../registry.ts'
import { headerKind, nonEmpty, splitOnce, statementsOf, words } from '../statements.ts'

export const er: Diagram = {
  kind: 'er',
  headers: ['erdiagram'],
  render(src) {
    const graph = parseEr(src)
    if (graph === null) return null
    const canvas = layoutClass(graph)
    if (canvas === null) return null
    return { canvas, warnings: graph.warnings, classDefs: graph.classDefs }
  },
}

export function parseEr(src: string): Graph | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !er.headers.includes(kind)) return null

  const graph = new Graph()
  /** The open `{` body: an entity index, `'skip'` for a dropped one, or none. */
  let curEntity: number | 'skip' | null = null

  for (const st of statements.slice(1)) {
    if (curEntity !== null) {
      if (st === '}') curEntity = null
      else if (curEntity !== 'skip') pushErAttribute(graph.nodes[curEntity], st)
      continue
    }

    const rel = splitErRelationship(st)
    if (rel) {
      const tokens = erTokens(rel.rel)
      const op = tokens.length === 3 ? parseErOp(tokens[1]) : null
      const f = op === null ? null : erEntity(graph, tokens[0])
      const t = f === null ? null : erEntity(graph, tokens[2])
      if (op === null || f === null || t === null) {
        graph.drop(st)
      } else {
        const relLabel = rel.label === null ? '' : cleanLabel(rel.label)
        graph.pushEdge({
          from: f,
          to: t,
          label: nonEmpty(relLabel),
          cardFrom: op.cardL,
          cardTo: op.cardR,
          headTo: 'none',
          headFrom: 'none',
          line: op.line,
        })
      }
    } else {
      const open = st.endsWith('{')
      const decl = open ? st.slice(0, -1).trim() : st
      if (decl === '' || erTokens(decl).length !== 1) {
        // A bad declaration that opened a body swallows it whole; reading the
        // attributes as top-level statements would misparse everything inside.
        graph.drop(st)
        if (open) curEntity = 'skip'
      } else {
        const idx = erEntity(graph, decl)
        if (open) curEntity = idx ?? 'skip'
      }
    }
    if (graph.truncated !== null) {
      graph.warnings.push(`diagram truncated: ${graph.truncated}`)
      break
    }
  }

  return graph.nodes.length === 0 ? null : graph
}

/** Resolve an entity token (`NAME`, `"Quoted Name"` or `id[Label]`), keeping
 * its title row fresh. A quoted name is its own identity; the quotes are not
 * part of the title. */
function erEntity(graph: Graph, token: string): number | null {
  const open = token.startsWith('"') ? -1 : token.indexOf('[')
  let idx: number | null
  if (open !== -1) {
    const id = token.slice(0, open)
    if (!token.endsWith(']')) {
      graph.warnings.push(`entity "${id}": alias is missing its closing \`]\``)
    }
    const label = cleanLabel(token.slice(open + 1).replace(/\]+$/, ''))
    if (id === '' || label === '') return null
    idx = graph.nodeLabel(id, label)
  } else if (token.startsWith('"')) {
    const label = cleanLabel(token)
    if (label === '') return null
    idx = graph.nodeIndex(token, label, 'rect')
  } else {
    idx = graph.nodeIndex(token, null, 'rect')
  }
  if (idx === null) return null
  const node = graph.nodes[idx]
  node.sections ??= [[], []]
  node.sections[0] = [displayGenerics(node.label)]
  return idx
}

/** Whitespace tokens, quoted spans kept whole so aliases may contain spaces. */
function erTokens(s: string): string[] {
  const out: string[] = []
  let cur = ''
  let inQuotes = false
  for (const c of s) {
    if (c === '"') {
      inQuotes = !inQuotes
      cur += c
    } else if (!inQuotes && /\s/.test(c)) {
      if (cur !== '') {
        out.push(cur)
        cur = ''
      }
    } else {
      cur += c
    }
  }
  if (cur !== '') out.push(cur)
  return out
}

function splitErRelationship(st: string): { rel: string; label: string | null } | null {
  const split = splitOnce(st, ':')
  const rel = split ? split[0] : st
  const label = split ? split[1].trim() : null
  return erTokens(rel).some((t) => parseErOp(t) !== null) ? { rel, label } : null
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
      return '*'
    case '}|':
    case '|{':
      return '1..*'
    default:
      return null
  }
}

/** ER attributes are `type name`; a trailing quoted comment is dropped. */
export function pushErAttribute(node: Node, raw: string): void {
  const attrs = (node.sections as string[][])[1]
  const parts: string[] = []
  for (const tok of words(raw)) {
    if (tok.startsWith('"')) break
    parts.push(tok)
  }
  if (parts.length === 0) return
  const line = decodeHtmlEntities(parts.join(' '))
  if (attrs.length < MAX_MEMBERS) attrs.push(line)
  else if (attrs.length === MAX_MEMBERS) attrs.push('…')
}
