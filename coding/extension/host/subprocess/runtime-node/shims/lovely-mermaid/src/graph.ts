/**
 * The shared diagram model. Flowchart, state, class and ER sources all parse
 * into a `Graph`; only sequence diagrams have their own model.
 */

import { asciiUpper } from './labels.ts'

/** Caps that keep layout bounded; exceeding one drops the diagram to fallback. */
export const MAX_NODES = 128
export const MAX_EDGES = 512
export const MAX_GROUPS = 24
export const MAX_GROUP_DEPTH = 6
/** Class members / ER attributes listed per box before eliding with `…`. */
export const MAX_MEMBERS = 8

export type Shape = 'rect' | 'round' | 'diamond'

/** Decoration at one end of an edge. */
export type Head =
  | 'none'
  | 'arrow'
  | 'circle'
  | 'cross'
  | 'triangle'
  | 'diamondFill'
  | 'diamondOpen'

export type LineKind = 'solid' | 'dotted' | 'thick'

export type Dir = 'down' | 'up' | 'right' | 'left'

export interface Node {
  label: string
  shape: Shape
  /**
   * Compartment content for class and ER boxes, pre-formatted by the parser:
   * one string per row, one array per compartment (title, attributes,
   * methods). Absent on plain nodes; layout draws whatever is here verbatim,
   * separated by horizontal rules.
   */
  sections?: string[][]
  /**
   * Author-assigned class names, from `:::name` or a `class A,B name`
   * statement. The renderer never interprets them; the cells the node paints
   * carry them out through `Span.classes`.
   */
  classes?: string[]
  /**
   * Link target from a `click A "url"` / `link A "url"` statement, carried
   * out through `Span.href` the way classes are; `toAnsi` emits it as an
   * OSC 8 hyperlink.
   */
  href?: string
}

export interface Edge {
  from: number
  to: number
  label: string | null
  /**
   * Cardinalities (ER crow's-foot, class multiplicities), painted at their
   * own end of the edge so position says which side a number belongs to.
   */
  cardFrom?: string
  cardTo?: string
  headTo: Head
  headFrom: Head
  line: LineKind
}

export interface Group {
  id: string
  label: string
  parent: number | null
}

/** `LR`/`RL`/`BT` as written in a header or `direction` statement; else `down`. */
export function parseDir(token: string): Dir {
  switch (asciiUpper(token)) {
    case 'LR':
      return 'right'
    case 'RL':
      return 'left'
    case 'BT':
      return 'up'
    default:
      return 'down'
  }
}

export class Graph {
  nodes: Node[] = []
  edges: Edge[] = []
  index = new Map<string, number>()
  groups: Group[] = []
  /** Innermost subgraph each node was declared in, parallel to `nodes`. */
  nodeGroup: (number | null)[] = []
  curGroup: number | null = null
  /**
   * Set when a size cap was hit, naming the cap. The parser stops consuming
   * statements and renders the prefix, warning about the truncation — a
   * streamed diagram that outgrows a cap can never shrink back under it, so
   * a stable truncated render beats flipping to the source box for good.
   */
  truncated: string | null = null
  /**
   * Source the grammar could not read and dropped. Parsing is lenient in
   * every grammar: a statement either contributes what parsed or is dropped
   * and recorded here, so the reader can tell a clean diagram from one that
   * is missing something they wrote.
   */
  warnings: string[] = []
  /** Parsed `classDef` declarations: name -> property map. */
  classDefs: Record<string, Record<string, string>> = {}
  dir: Dir = 'down'

  constructor(dir: Dir = 'down') {
    this.dir = dir
  }

  /**
   * Index of `id`, creating the node if new. A later declaration carrying a
   * label overwrites the placeholder one an edge created. Returns `null` once
   * `MAX_NODES` is reached, which truncates the parse.
   */
  nodeIndex(id: string, label: string | null, shape: Shape): number | null {
    const existing = this.index.get(id)
    if (existing !== undefined) {
      if (label !== null) {
        this.nodes[existing].label = label
        this.nodes[existing].shape = shape
      }
      return existing
    }
    if (this.nodes.length >= MAX_NODES) {
      this.truncated ??= `node cap (${MAX_NODES}) reached`
      return null
    }
    this.index.set(id, this.nodes.length)
    this.nodes.push({ label: label ?? id, shape })
    this.nodeGroup.push(this.curGroup)
    return this.nodes.length - 1
  }

  /** Set a node's label without disturbing its shape, creating it if new. */
  nodeLabel(id: string, label: string): number | null {
    const existing = this.index.get(id)
    if (existing !== undefined) {
      this.nodes[existing].label = label
      return existing
    }
    return this.nodeIndex(id, label, 'round')
  }

  /** Attach an author class name to a node, ignoring a repeat. */
  addClass(idx: number, name: string): void {
    const node = this.nodes[idx]
    node.classes ??= []
    if (!node.classes.includes(name)) node.classes.push(name)
  }

  /**
   * Apply collected `[ids, names]` class assignments. Run after the statement
   * walk so a `class A,B name` (or `:::` tag) may precede the nodes it names;
   * unknown ids are ignored.
   */
  applyClasses(assignments: [string[], string[]][]): void {
    for (const [ids, names] of assignments) {
      for (const id of ids) {
        const idx = this.index.get(id.trim())
        if (idx === undefined) continue
        for (const name of names) {
          if (name.trim() !== '') this.addClass(idx, name.trim())
        }
      }
    }
  }

  /** Apply `[id, url]` link targets; the last one per id wins, unknown ids
   * are ignored. Deferred like `applyClasses`, for the same ordering reason. */
  applyHrefs(hrefs: [string, string][]): void {
    for (const [id, url] of hrefs) {
      const idx = this.index.get(id.trim())
      if (idx !== undefined) this.nodes[idx].href = url
    }
  }

  /**
   * Record an unreadable statement; the diagram renders without it. Skipped
   * once truncated — a cap-caused parse failure is not the statement's fault.
   */
  drop(st: string): void {
    if (this.truncated === null) this.warnings.push(`dropped, unreadable statement: "${st}"`)
  }

  /** Append an edge, or flag `truncated` when `MAX_EDGES` is reached. */
  pushEdge(edge: Edge): boolean {
    if (this.edges.length >= MAX_EDGES) {
      this.truncated ??= `edge cap (${MAX_EDGES}) reached`
      return false
    }
    this.edges.push(edge)
    return true
  }
}
