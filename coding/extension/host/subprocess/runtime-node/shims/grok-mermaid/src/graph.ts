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
}

export interface Edge {
  from: number
  to: number
  label: string | null
  headTo: Head
  headFrom: Head
  line: LineKind
}

export interface Group {
  id: string
  label: string
  parent: number | null
}

/** Extra compartment content for class and ER boxes. */
export interface ClassInfo {
  annotation: string | null
  attrs: string[]
  methods: string[]
}

export const emptyClassInfo = (): ClassInfo => ({ annotation: null, attrs: [], methods: [] })

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
  /** Set when a cap was hit; the caller abandons the parse. */
  overCap = false
  /**
   * Text the flowchart grammar could not read and silently discarded.
   *
   * Flowchart parsing is deliberately lenient — a malformed statement
   * contributes whatever prefix parsed and the rest is dropped — so without
   * these the reader gets a clean diagram that is not what they wrote.
   */
  warnings: string[] = []
  dir: Dir = 'down'

  constructor(dir: Dir = 'down') {
    this.dir = dir
  }

  /**
   * Index of `id`, creating the node if new. A later declaration carrying a
   * label overwrites the placeholder one an edge created. Returns `null` once
   * `MAX_NODES` is reached, which aborts the parse.
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
      this.overCap = true
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

  /** Append an edge, or flag `overCap` when `MAX_EDGES` is reached. */
  pushEdge(edge: Edge): boolean {
    if (this.edges.length >= MAX_EDGES) {
      this.overCap = true
      return false
    }
    this.edges.push(edge)
    return true
  }
}
