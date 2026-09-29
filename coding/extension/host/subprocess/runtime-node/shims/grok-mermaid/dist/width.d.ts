/**
 * Columns occupied by one grapheme cluster.
 *
 * The widest code point wins, so a base plus its combining marks measures as
 * the base. Two adjustments: a variation selector requesting emoji
 * presentation forces two columns, as does a regional indicator pair (a flag).
 *
 * Zero is a real answer — a soft hyphen or zero-width space occupies nothing,
 * and callers skip painting such a cluster rather than reserving a cell.
 */
export declare function clusterWidth(cluster: string): number;
/** Iterate grapheme clusters, so no loop can split one. */
export declare function clusters(s: string): Generator<string>;
/** Iterate clusters paired with their display width. */
export declare function measured(s: string): Generator<[string, number]>;
/** Display columns of a string. */
export declare function stringWidth(s: string): number;
//# sourceMappingURL=width.d.ts.map