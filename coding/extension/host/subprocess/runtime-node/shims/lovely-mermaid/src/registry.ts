/**
 * The diagram registry: one entry per supported diagram type.
 *
 * `diagramKind` and `render` both resolve through this table, so the header
 * test each parser gates on and the one `diagramKind` reports are the same
 * function by construction. Adding a diagram type is one module in
 * `diagrams/` plus one entry here.
 */

import type { Canvas } from './canvas.ts'
import { classDiagram } from './diagrams/class.ts'
import { er } from './diagrams/er.ts'
import { flowchart } from './diagrams/flowchart.ts'
import { gitgraph } from './diagrams/gitgraph.ts'
import { mindmap } from './diagrams/mindmap.ts'
import { pie } from './diagrams/pie.ts'
import { sequence } from './diagrams/sequence.ts'
import { state } from './diagrams/state.ts'
import { timeline } from './diagrams/timeline.ts'
import { stripControls } from './labels.ts'
import { headerKind, statementsOf } from './statements.ts'

/** A diagram type this renderer draws. */
export type DiagramKind =
  | 'flowchart'
  | 'state'
  | 'class'
  | 'er'
  | 'sequence'
  | 'pie'
  | 'mindmap'
  | 'timeline'
  | 'gitgraph'

export interface Diagram {
  kind: DiagramKind
  /**
   * Header keywords declaring this diagram type, lowercased. Matched exactly:
   * upstream's prefix tests accept junk like `stateDiagramFoo` that mermaid
   * proper rejects at the grammar stage.
   */
  headers: string[]
  /** Parse and lay out; `null` means nothing was drawn. */
  render(src: string): {
    canvas: Canvas
    warnings: string[]
    classDefs: Record<string, Record<string, string>>
  } | null
}

export const DIAGRAMS: Diagram[] = [
  flowchart,
  state,
  classDiagram,
  er,
  sequence,
  pie,
  mindmap,
  timeline,
  gitgraph,
]

/** The registry entry `src`'s header declares, or `null`. */
export function diagramFor(src: string): Diagram | null {
  const header = headerKind(statementsOf(src))
  if (header === null) return null
  return DIAGRAMS.find((d) => d.headers.includes(header)) ?? null
}

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
 */
export function diagramKind(src: string): DiagramKind | null {
  // The same strip `render` applies, so the two entry points agree on any src.
  return diagramFor(stripControls(src))?.kind ?? null
}
