/**
 * `sequenceDiagram`: participants, messages, notes and block dividers.
 * Lenient: an unreadable statement is dropped and recorded in `warnings`.
 *
 * Sequence diagrams have their own model — participants in declaration order
 * plus a flat list of items — and their own geometry in `layout-seq.ts`.
 */
import type { Diagram } from '../registry.ts';
export declare const sequence: Diagram;
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
/** A hot span of one lifeline: item indices, `to === null` while still open. */
export interface Activation {
    at: number;
    from: number;
    to: number | null;
}
export declare class Sequence {
    labels: string[];
    index: Map<string, number>;
    items: SeqItem[];
    activations: Activation[];
    /** Set when a size cap was hit; the parser stops and renders the prefix. */
    truncated: string | null;
    /** Statements the grammar could not read and dropped. */
    warnings: string[];
    participant(id: string, label: string | null): number | null;
    /** Append an item, or flag `truncated` when the item cap is reached. */
    pushItem(item: SeqItem): void;
    /** Record an unreadable statement; skipped once truncated. */
    drop(st: string): void;
    /** Open an activation at item `from`. */
    activate(at: number, from: number): void;
    /** Close the innermost open activation of `at` at item `to`. */
    deactivate(at: number, to: number): void;
}
export declare function parseSequence(src: string): Sequence | null;
//# sourceMappingURL=sequence.d.ts.map