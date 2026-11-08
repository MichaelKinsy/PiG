/**
 * `stateDiagram` / `stateDiagram-v2`: states, transitions, descriptions and
 * composite states. Lenient: an unreadable statement is dropped and recorded
 * in `warnings`.
 *
 * A composite (`state X { ... }`) becomes a `Group`, drawn as a titled frame
 * by the same machinery flowchart subgraphs use; `--` splits a composite into
 * unlabelled sibling region groups. `[*]` is scoped per group, so an inner
 * start dot is a different node from the outer one.
 */

import { Graph, MAX_GROUP_DEPTH, MAX_GROUPS, parseDir, type Shape } from '../graph.ts'
import { asciiLower, decodeHtmlEntities } from '../labels.ts'
import { layoutFlowchart, layoutGrouped } from '../layout.ts'
import type { Diagram } from '../registry.ts'
import {
  firstWord,
  headerKind,
  nonEmpty,
  parseClassAssign,
  parseClassDef,
  splitColon,
  splitOnce,
  statementsOf,
  takeTags,
  words,
} from '../statements.ts'

export const state: Diagram = {
  kind: 'state',
  headers: ['statediagram', 'statediagram-v2'],
  render(src) {
    const graph = parseState(src)
    if (graph === null) return null
    const canvas = graph.groups.length === 0 ? layoutFlowchart(graph) : layoutGrouped(graph)
    if (canvas === null) return null
    return { canvas, warnings: graph.warnings, classDefs: graph.classDefs }
  },
}

export function parseState(src: string): Graph | null {
  const statements = statementsOf(src)
  const kind = headerKind(statements)
  if (kind === null || !state.headers.includes(kind)) return null

  const graph = new Graph()
  let inNote = false
  /** `class A,B name` assignments, applied after the walk. */
  const classAssignments: [string[], string[]][] = []
  /** Open composites: the group itself and its current `--` region, if any. */
  const stack: { base: number; region: number | null }[] = []

  /** New group under the current scope, or `null` once a cap is hit. */
  const newGroup = (id: string, label: string): number | null => {
    if (graph.groups.length >= MAX_GROUPS || stack.length >= MAX_GROUP_DEPTH) {
      graph.truncated ??= `subgraph cap (${MAX_GROUPS} groups, depth ${MAX_GROUP_DEPTH}) reached`
      return null
    }
    graph.groups.push({ id, label, parent: graph.curGroup })
    return graph.groups.length - 1
  }
  const scopeOf = (): number | null => {
    const top = stack.at(-1)
    return top === undefined ? null : (top.region ?? top.base)
  }

  for (const st of statements.slice(1)) {
    if (inNote) {
      if (asciiLower(st) === 'end note') inNote = false
      continue
    }
    const first = asciiLower(firstWord(st))
    if (first === 'direction') {
      graph.dir = parseDir(words(st)[1] ?? '')
    } else if (first === 'note') {
      // A single-line `note ... : text` needs no terminator.
      if (!st.includes(':')) inNote = true
    } else if (first === 'state') {
      const rest = st.slice(firstWord(st).length).trim()
      const open = rest.endsWith('{')
      const body = open ? rest.slice(0, -1).trim() : rest
      if (!open) {
        if (parseStateDecl(body, graph) === null) graph.drop(st)
      } else {
        // A composite. An unreadable declaration still opens an anonymous
        // frame: the `{` was consumed, so the `}` balance must hold.
        const named = compositeName(body)
        if (named === null) graph.drop(st)
        const gi = newGroup(named?.id ?? `anon ${graph.groups.length}`, named?.label ?? '')
        if (gi !== null) {
          stack.push({ base: gi, region: null })
          graph.curGroup = gi
        }
      }
    } else if (first === '}') {
      stack.pop()
      graph.curGroup = scopeOf()
    } else if (first === '--') {
      // Region divider: members so far move into region 1 on the first `--`;
      // each divider opens the next unlabelled sibling region.
      const top = stack.at(-1)
      if (top !== undefined) {
        if (top.region === null) {
          const r1 = newGroup(`region ${graph.groups.length}`, '')
          if (r1 !== null) {
            graph.nodeGroup.forEach((g, i) => {
              if (g === top.base) graph.nodeGroup[i] = r1
            })
            graph.groups.forEach((g, i) => {
              if (i !== r1 && g.parent === top.base) g.parent = r1
            })
          }
        }
        graph.curGroup = top.base
        const next = newGroup(`region ${graph.groups.length}`, '')
        top.region = next
        graph.curGroup = next ?? top.base
      }
    } else if (first === 'classdef') {
      const def = parseClassDef(st.slice(firstWord(st).length))
      if (def) for (const name of def.names) graph.classDefs[name] = def.props
    } else if (first === 'class') {
      const assign = parseClassAssign(st.slice(firstWord(st).length))
      if (assign) classAssignments.push(assign)
    } else if (['hide', 'scale'].includes(first)) {
      // Styling directives carry no layout meaning.
    } else if (st.includes('-->')) {
      if (parseTransition(st, graph) === null) graph.drop(st)
    } else if (parseStateDesc(st, graph) === null) {
      graph.drop(st)
    }
    if (graph.truncated !== null) {
      graph.warnings.push(`diagram truncated: ${graph.truncated}`)
      break
    }
  }

  graph.applyClasses(classAssignments)

  return graph.nodes.length === 0 ? null : graph
}

/** The id and label of a composite declaration body, or `null`. */
function compositeName(body: string): { id: string; label: string } | null {
  if (body.startsWith('"')) {
    const close = body.indexOf('"', 1)
    if (close === -1) return null
    const label = decodeHtmlEntities(body.slice(1, close))
    const after = body.slice(close + 1).trim()
    // A `:::` tag on a composite is dropped: groups paint no classed cells.
    const id = takeTags(after.startsWith('as') ? after.slice(2).trim() : label).id
    return id === '' ? null : { id, label }
  }
  // A stereotype on a composite carries no drawing of its own; keep the name.
  const id = takeTags(body.split('<<')[0].trim()).id
  return id === '' || /\s/.test(id) ? null : { id, label: id }
}

/** `state "Label" as id` or `state id <<choice>>` — the non-composite forms. */
function parseStateDecl(rest: string, graph: Graph): true | null {
  if (rest === '') return true

  if (rest.startsWith('"')) {
    const close = rest.indexOf('"', 1)
    if (close === -1) return null
    const label = rest.slice(1, close)
    const after = rest.slice(close + 1).trim()
    const { id, classes } = takeTags(after.startsWith('as') ? after.slice(2).trim() : label)
    const idx = graph.nodeLabel(id, decodeHtmlEntities(label))
    if (idx === null) return null
    for (const cls of classes) graph.addClass(idx, cls)
    return true
  }

  let shape: Shape = 'round'
  let token = rest
  let stereotyped = false
  const pos = rest.indexOf('<<')
  if (pos !== -1) {
    const stereo = rest
      .slice(pos + 2)
      .replace(/>>$/, '')
      .trim()
    if (stereo === 'choice') shape = 'diamond'
    token = rest.slice(0, pos).trim()
    stereotyped = true
  }
  const { id, classes } = takeTags(token)
  if (id === '' || /\s/.test(id)) return null
  const idx = graph.nodeIndex(id, stereotyped ? id : null, shape)
  if (idx === null) return null
  for (const cls of classes) graph.addClass(idx, cls)
  return true
}

/** `A --> B: label`, including chains `A --> B --> C`. */
function parseTransition(st: string, graph: Graph): true | null {
  let rest = st
  let prev: number | null = null

  for (;;) {
    const split = splitOnce(rest, '-->')
    if (!split) break
    const [lhs, rhs] = split

    const fromTok = takeTags(lhs.trimEnd().replace(/-+$/, '').trim())
    let from: number
    if (prev !== null) {
      // Mid-chain: the source is the previous target, so nothing may precede.
      if (fromTok.id !== '') return null
      from = prev
    } else {
      if (fromTok.id === '') return null
      const f = stateEndpoint(graph, fromTok.id, true)
      if (f === null) return null
      for (const cls of fromTok.classes) graph.addClass(f, cls)
      from = f
    }

    // After the label colon it is all label — mermaid never chains past a
    // label, so an arrow inside one (`: go "x --> y"`) is text, not a link.
    const nextArrow = rhs.indexOf('-->')
    const colon = splitColon(rhs)
    const labelFirst = colon !== null && (nextArrow === -1 || colon[0].length < nextArrow)
    const toPart = labelFirst ? colon[0] : nextArrow === -1 ? rhs : rhs.slice(0, nextArrow)
    const label = labelFirst ? nonEmpty(decodeHtmlEntities(colon[1].trim())) : null
    const tail = labelFirst || nextArrow === -1 ? '' : rhs.slice(nextArrow)

    const toTok = takeTags(
      toPart.trimStart().replace(/^>+/, '').trimEnd().replace(/-+$/, '').trim(),
    )
    if (toTok.id === '') return null
    const to = stateEndpoint(graph, toTok.id, false)
    if (to === null) return null
    for (const cls of toTok.classes) graph.addClass(to, cls)

    if (!graph.pushEdge({ from, to, label, headTo: 'arrow', headFrom: 'none', line: 'solid' })) {
      return true
    }
    prev = to
    rest = tail
  }
  return true
}

/**
 * `[*]` is start or end depending on which side of the arrow it sits, and is
 * scoped to the enclosing composite — each frame has its own start and end.
 */
function stateEndpoint(graph: Graph, id: string, isSource: boolean): number | null {
  if (id === '[*]') {
    const scope = graph.curGroup === null ? '' : ` g${graph.curGroup}`
    return graph.nodeIndex(`[*]${isSource ? 'start' : 'end'}${scope}`, '●', 'round')
  }
  return graph.nodeIndex(id, null, 'round')
}

/** `id: description`, or a bare state name; either may carry `:::` tags. */
function parseStateDesc(st: string, graph: Graph): true | null {
  const split = splitColon(st)
  const { id, classes } = takeTags((split ? split[0] : st).trim())
  let idx: number | null
  if (split) {
    const desc = split[1].trim()
    if (id === '' || /\s/.test(id) || desc === '') return null
    // Repeated descriptions accumulate (mermaid stacks them as lines; here
    // they join and wrap). A bare node's label is its id — that one replaces.
    const prev = graph.index.get(id)
    const before = prev === undefined ? null : graph.nodes[prev].label
    const text = decodeHtmlEntities(desc)
    idx = graph.nodeLabel(id, before !== null && before !== id ? `${before} ${text}` : text)
  } else {
    if (id === '' || /\s/.test(id)) return null
    idx = graph.nodeIndex(id, null, 'round')
  }
  if (idx === null) return null
  for (const cls of classes) graph.addClass(idx, cls)
  return true
}
