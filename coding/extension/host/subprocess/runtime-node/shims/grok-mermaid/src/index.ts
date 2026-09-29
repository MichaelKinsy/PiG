import { stripControls } from './labels.ts'
import { type CanvasResult, layoutClass, layoutFlowchart, layoutGrouped } from './layout.ts'
import { layoutSequence } from './layout-seq.ts'
import { diagramKind, parseClass, parseEr, parseGraph, parseSequence, parseState } from './parse.ts'
import type { MermaidArt } from './types.ts'

export { type AnsiTheme, DEFAULT_THEME, toAnsi } from './ansi.ts'
export { type DiagramKind, diagramKind } from './parse.ts'
export { sourceBox } from './source-box.ts'
export type { Cls, MermaidArt, Span } from './types.ts'

/**
 * Render a Mermaid source block as Unicode box-drawing art.
 *
 * Supported: `graph`/`flowchart` (including `subgraph`), `stateDiagram`,
 * `classDiagram`, `erDiagram` and `sequenceDiagram`.
 *
 * The diagram is laid out at whatever size it needs; `art.width` reports the
 * columns that turned out to be. Deciding what to do when that exceeds the
 * space at hand is the caller's — `sourceBox` is the usual answer:
 *
 * ```ts
 * const art = render(src)
 * show(art && art.width <= cols ? art : sourceBox(src, cols))
 * ```
 *
 * `null` means there is no art to show: blank input, a syntax error, a diagram
 * type this renderer does not draw, or one large enough that laying it out is
 * refused. `diagramKind` separates the middle two.
 *
 * Rendering is best-effort. A flowchart keeps whatever parsed; the stricter
 * grammars additionally get one retry without their final line, which is what
 * keeps a streaming diagram on screen while its last statement is half-typed.
 * Everything given up on is listed in `art.warnings` — advisory only, never a
 * reason to withhold the art.
 */
export function render(src: string): MermaidArt | null {
  src = stripControls(src)
  if (src.trim() === '') return null
  const drawn = attempt(src)
  if (drawn === null) return null
  return { ...drawn.canvas.toLines(), warnings: drawn.warnings }
}

type Drawn = { canvas: NonNullable<CanvasResult>; warnings: string[] }

/**
 * Draw `src`, retrying once without its last line if the grammar rejects it.
 *
 * State, class, ER and sequence fail a whole diagram on one unreadable
 * statement, and while a source is streaming its last line is usually still
 * being typed — so without this a diagram alternates with the source box all
 * the way in. Only the final line is dropped, and doing so is always reported,
 * so a finished document with a bad last line still says what it lost rather
 * than quietly rendering short.
 */
function attempt(src: string): Drawn | null {
  const drawn = draw(src)
  if (drawn !== null) return drawn

  const body = src.replace(/\s+$/, '')
  const cut = body.lastIndexOf('\n')
  if (cut === -1) return null
  const salvaged = draw(body.slice(0, cut))
  if (salvaged === null) return null

  const dropped = body.slice(cut + 1).trim()
  return {
    canvas: salvaged.canvas,
    warnings: [...salvaged.warnings, `dropped, unreadable final line: "${dropped}"`],
  }
}

/** Dispatch on the declared diagram type; `null` means nothing was drawn. */
function draw(src: string): Drawn | null {
  const plain = (canvas: CanvasResult) => (canvas === null ? null : { canvas, warnings: [] })

  switch (diagramKind(src)) {
    case 'flowchart': {
      const graph = parseGraph(src)
      if (graph === null) return null
      const canvas = graph.groups.length === 0 ? layoutFlowchart(graph) : layoutGrouped(graph)
      return canvas === null ? null : { canvas, warnings: graph.warnings }
    }
    case 'state': {
      const state = parseState(src)
      return state === null ? null : plain(layoutFlowchart(state))
    }
    case 'class': {
      const cls = parseClass(src)
      return cls === null ? null : plain(layoutClass(cls.graph, cls.infos))
    }
    case 'er': {
      const er = parseEr(src)
      return er === null ? null : plain(layoutClass(er.graph, er.infos))
    }
    case 'sequence': {
      const seq = parseSequence(src)
      return seq === null ? null : plain(layoutSequence(seq))
    }
    default:
      return null
  }
}
