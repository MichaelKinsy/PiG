/**
 * Semantic role of a run of cells — what a cell *is*, decided by the
 * renderer. The renderer never knows about colour; consumers map roles to
 * their own theme (see `toAnsi` for the common case).
 *
 * - `border`     box outlines, subgraph frames, compartment rules
 * - `text`       node / participant / compartment labels
 * - `edge`       connector lines and arrowheads
 * - `edgeLabel`  text sitting on an edge
 * - `title`      the `mermaid: <kind>` header of a source box
 * - `none`       blank filler
 *
 * Distinct from `classes`, which is what the *author* assigned.
 */
export type Role = 'border' | 'text' | 'edge' | 'edgeLabel' | 'title' | 'none'

/** A run of adjacent cells sharing one role and one set of author classes. */
export interface Span {
  text: string
  role: Role
  /**
   * Author-assigned class names of the node these cells belong to, from a
   * `:::name` shorthand or a `class A,B name` statement. The renderer never
   * interprets them — pair with `MermaidArt.classDefs` to style. Absent on
   * cells that belong to no classed node.
   */
  classes?: string[]
  /**
   * Link target of the node these cells belong to, from a `click A "url"`
   * (flowchart) or `link A "url"` (class diagram) statement. `toAnsi` emits
   * it as an OSC 8 hyperlink; other consumers map it to their own linking.
   */
  href?: string
}

/**
 * A rendered diagram. `plain[i]` and `styled[i]` describe the same row:
 * `plain` is right-trimmed for display width and copy/paste, `styled` keeps
 * the run structure needed to colour it.
 *
 * `width` is the display columns the widest row needs — the number to compare
 * against the space you have. It cannot be recovered from `plain`, whose rows
 * are strings of code points, not columns.
 *
 * `classDefs` are the diagram's `classDef` declarations, parsed:
 * `classDef warning fill:#f96,stroke:#333` becomes
 * `{ warning: { fill: '#f96', stroke: '#333' } }`. The renderer ignores them;
 * a consumer can map them onto its own styling of the classes spans carry.
 *
 * `warnings` lists source the grammar could not read and dropped, and any
 * size-cap truncation. Non-empty means the art is real but incomplete — some
 * of what was written is not in it.
 *
 * They are advisory. Do not gate rendering on them: the art is the best drawing
 * of the source either way, and a diagram being typed or streamed warns at
 * nearly every intermediate state. Show them alongside, or once it settles.
 */
export interface MermaidArt {
  plain: string[]
  styled: Span[][]
  width: number
  classDefs: Record<string, Record<string, string>>
  warnings: string[]
}
