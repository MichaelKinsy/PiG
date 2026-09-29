/**
 * Source text to diagram model.
 *
 * Every `parseX` returns `null` when the source is not that kind of diagram,
 * or when it exceeds a cap — `render` tries each in turn and falls back to a
 * framed copy of the source when they all decline.
 */
import { type ClassInfo, Graph } from './graph.ts';
/**
 * Split one source line into statements on `;`, stopping at a `%%` comment.
 *
 * Quoted spans are opaque, so a label may contain `;` and `%%`.
 */
export declare function splitStatements(line: string, out: string[]): void;
/** All statements in a source block, in order. */
export declare function statementsOf(src: string): string[];
/** A diagram type this renderer draws. */
export type DiagramKind = 'flowchart' | 'state' | 'class' | 'er' | 'sequence';
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
 *
 * Each branch mirrors the header test in the matching `parseX`, so the two
 * always agree on what they recognise.
 */
export declare function diagramKind(src: string): DiagramKind | null;
export declare function parseGraph(src: string): Graph | null;
export declare function parseState(src: string): Graph | null;
export declare function parseClass(src: string): {
    graph: Graph;
    infos: ClassInfo[];
} | null;
/** Add a member to the attribute or method compartment, eliding past the cap. */
export declare function pushMember(info: ClassInfo, raw: string): void;
export declare function parseEr(src: string): {
    graph: Graph;
    infos: ClassInfo[];
} | null;
/** ER attributes are `type name`; a trailing quoted comment is dropped. */
export declare function pushErAttribute(info: ClassInfo, raw: string): void;
export type SeqHead = 'arrow' | 'cross';
export type NoteAnchor = {
    kind: 'over';
    from: number;
    to: number;
} | {
    kind: 'left';
    at: number;
} | {
    kind: 'right';
    at: number;
};
export type SeqItem = {
    kind: 'message';
    from: number;
    to: number;
    text: string | null;
    dashed: boolean;
    head: SeqHead;
} | {
    kind: 'note';
    anchor: NoteAnchor;
    text: string;
} | {
    kind: 'divider';
    text: string;
};
export declare class Sequence {
    labels: string[];
    index: Map<string, number>;
    items: SeqItem[];
    participant(id: string, label: string | null): number | null;
}
export declare function parseSequence(src: string): Sequence | null;
//# sourceMappingURL=parse.d.ts.map