import type { Role, Span } from './types.ts';
/**
 * Sentinel occupying the trailing column of a wide glyph. Never emitted: the
 * line builder skips it so a CJK character claims two cells of layout but
 * contributes one character of output.
 */
export declare const CONT: string;
/** Connection direction bits, combined into a box-drawing glyph by `maskChar`. */
export declare const U = 1;
export declare const D = 2;
export declare const L = 4;
export declare const R = 8;
/** Line styles, tracked per cell so crossing edges keep their own stroke. */
export declare const STY_DOT = 1;
export declare const STY_THICK = 2;
export declare const STY_SOLID = 4;
/**
 * A grid of cells. Edges accumulate as direction bits rather than glyphs so
 * that crossings and junctions resolve correctly whatever order they are drawn
 * in; `finalizeMask` turns the accumulated bits into characters at the end.
 *
 * `occupied` marks cells claimed by a box, which edge bits must not overwrite.
 */
export declare class Canvas {
    readonly w: number;
    readonly h: number;
    ch: string[];
    role: Role[];
    /** Space-joined author classes per cell, or undefined; see `Span.classes`. */
    tag: (string | undefined)[];
    /** Link target per cell, or undefined; see `Span.href`. */
    href: (string | undefined)[];
    mask: Uint8Array;
    style: Uint8Array;
    occupied: Uint8Array;
    curStyle: number;
    /** Author classes stamped on cells painted while set, like `curStyle`. */
    curTag: string | undefined;
    /** Link target stamped on cells painted while set, like `curTag`. */
    curHref: string | undefined;
    constructor(w: number, h: number);
    idx(x: number, y: number): number;
    set(x: number, y: number, c: string, role: Role): void;
    /**
     * Accumulate direction bits on a free cell.
     *
     * `role` is the role to claim the cell for; `border` cells are never
     * reclassified, so a connector meeting a box keeps the box's styling.
     */
    addBits(x: number, y: number, bits: number, role?: Role): void;
    /** Stamp a finished sub-canvas (a subgraph frame's contents) at an offset. */
    blit(sub: Canvas, ox: number, oy: number): void;
    /** Add direction bits even to an occupied cell, so an edge can meet a border. */
    junction(x: number, y: number, bits: number): void;
    segV(x: number, y0: number, y1: number): void;
    segH(y: number, x0: number, x1: number): void;
    /** Resolve accumulated direction bits into glyphs, honouring line style. */
    finalizeMask(): void;
    /**
     * Mirror top-to-bottom for `BT`. Rows reorder but within-row text does not,
     * so labels stay readable; box-drawing glyphs flip to match.
     */
    flipVertical(): void;
    /**
     * Mirror left-to-right for `RL`. Mirroring reverses each row, so after
     * flipping glyphs each text/label run is reversed back to reading order.
     */
    flipHorizontal(): void;
    /** Group each row into runs of one role and tag, dropping continuations. */
    toLines(): {
        plain: string[];
        styled: Span[][];
        width: number;
    };
}
/**
 * Paint `text` at `x, y`, one grapheme cluster per cell.
 *
 * A wide cluster claims a second cell, marked with `CONT` so the line builder
 * emits one character for it rather than a stray space.
 */
export declare function drawText(canvas: Canvas, text: string, x: number, y: number, role: Role): void;
/**
 * Paint `text` at `x, y`, clearing any edge bits underneath first.
 *
 * Used where text sits on top of a drawn line (sequence messages, dividers,
 * compartment rows) and must win over it.
 */
export declare function drawTextOverEdges(canvas: Canvas, text: string, x: number, y: number, role: Role): void;
export declare function maskChar(mask: number): string;
//# sourceMappingURL=canvas.d.ts.map