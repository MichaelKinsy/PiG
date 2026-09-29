import type { Cls, Span } from './types.ts';
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
    cls: Cls[];
    mask: Uint8Array;
    style: Uint8Array;
    occupied: Uint8Array;
    curStyle: number;
    constructor(w: number, h: number);
    idx(x: number, y: number): number;
    set(x: number, y: number, c: string, cls: Cls): void;
    /**
     * Accumulate direction bits on a free cell.
     *
     * `cls` is the class to claim the cell for; `border` cells are never
     * reclassified, so a connector meeting a box keeps the box's styling.
     */
    addBits(x: number, y: number, bits: number, cls?: Cls): void;
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
    /** Group each row into runs of one class, dropping wide-glyph continuations. */
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
export declare function drawText(canvas: Canvas, text: string, x: number, y: number, cls: Cls): void;
/**
 * Paint `text` at `x, y`, clearing any edge bits underneath first.
 *
 * Used where text sits on top of a drawn line (sequence messages, dividers,
 * compartment rows) and must win over it.
 */
export declare function drawTextOverEdges(canvas: Canvas, text: string, x: number, y: number, cls: Cls): void;
export declare function maskChar(mask: number): string;
export declare const dottedChar: (c: string) => string;
export declare const thickChar: (c: string) => string;
export declare const flipGlyphV: (c: string) => string;
export declare const flipGlyphH: (c: string) => string;
//# sourceMappingURL=canvas.d.ts.map