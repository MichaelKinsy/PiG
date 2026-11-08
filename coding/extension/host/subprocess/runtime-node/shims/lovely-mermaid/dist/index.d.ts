import type { MermaidArt } from './types.ts';
export { type AnsiTheme, classSgr, DEFAULT_THEME, toAnsi } from './ansi.ts';
export { type ClassStyle, contrastOn, resolveClassStyle } from './class-style.ts';
export { type DiagramKind, diagramKind } from './registry.ts';
export { sourceBox } from './source-box.ts';
export type { MermaidArt, Role, Span } from './types.ts';
/**
 * Render a Mermaid source block as Unicode box-drawing art.
 *
 * Supported: `graph`/`flowchart` (including `subgraph`), `stateDiagram`,
 * `classDiagram`, `erDiagram`, `sequenceDiagram`, `pie`, `mindmap`,
 * `timeline` and `gitGraph`.
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
 * `null` means there is no art to show: blank input, a diagram type this
 * renderer does not draw, a source in which not one statement parsed, or a
 * diagram large enough that laying it out is refused. `diagramKind` separates
 * the middle two.
 *
 * Rendering is best-effort in every grammar: a statement either contributes
 * what parsed or is dropped, and a diagram over a size cap renders its prefix.
 * Everything given up on is listed in `art.warnings` — advisory only, never a
 * reason to withhold the art.
 */
export declare function render(src: string): MermaidArt | null;
//# sourceMappingURL=index.d.ts.map