/**
 * The diagram registry: one entry per supported diagram type.
 *
 * `diagramKind` and `render` both resolve through this table, so the header
 * test each parser gates on and the one `diagramKind` reports are the same
 * function by construction. Adding a diagram type is one module in
 * `diagrams/` plus one entry here.
 */
import type { Canvas } from './canvas.ts';
/** A diagram type this renderer draws. */
export type DiagramKind = 'flowchart' | 'state' | 'class' | 'er' | 'sequence' | 'pie' | 'mindmap' | 'timeline' | 'gitgraph';
export interface Diagram {
    kind: DiagramKind;
    /**
     * Header keywords declaring this diagram type, lowercased. Matched exactly:
     * upstream's prefix tests accept junk like `stateDiagramFoo` that mermaid
     * proper rejects at the grammar stage.
     */
    headers: string[];
    /** Parse and lay out; `null` means nothing was drawn. */
    render(src: string): {
        canvas: Canvas;
        warnings: string[];
        classDefs: Record<string, Record<string, string>>;
    } | null;
}
export declare const DIAGRAMS: Diagram[];
/** The registry entry `src`'s header declares, or `null`. */
export declare function diagramFor(src: string): Diagram | null;
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
export declare function diagramKind(src: string): DiagramKind | null;
//# sourceMappingURL=registry.d.ts.map