import type { MermaidArt } from './types.ts';
export { type AnsiTheme, DEFAULT_THEME, toAnsi } from './ansi.ts';
export { type DiagramKind, diagramKind } from './parse.ts';
export { sourceBox } from './source-box.ts';
export type { Cls, MermaidArt, Span } from './types.ts';
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
export declare function render(src: string): MermaidArt | null;
//# sourceMappingURL=index.d.ts.map