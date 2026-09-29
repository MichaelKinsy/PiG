import { measured } from './width.js';
/**
 * Sentinel occupying the trailing column of a wide glyph. Never emitted: the
 * line builder skips it so a CJK character claims two cells of layout but
 * contributes one character of output.
 */
export const CONT = String.fromCharCode(0);
/** Connection direction bits, combined into a box-drawing glyph by `maskChar`. */
export const U = 1;
export const D = 2;
export const L = 4;
export const R = 8;
/** Line styles, tracked per cell so crossing edges keep their own stroke. */
export const STY_DOT = 1;
export const STY_THICK = 2;
export const STY_SOLID = 4;
/**
 * A grid of cells. Edges accumulate as direction bits rather than glyphs so
 * that crossings and junctions resolve correctly whatever order they are drawn
 * in; `finalizeMask` turns the accumulated bits into characters at the end.
 *
 * `occupied` marks cells claimed by a box, which edge bits must not overwrite.
 */
export class Canvas {
    w;
    h;
    ch;
    cls;
    mask;
    style;
    occupied;
    curStyle = STY_SOLID;
    constructor(w, h) {
        const n = w * h;
        this.w = w;
        this.h = h;
        this.ch = new Array(n).fill(' ');
        this.cls = new Array(n).fill('none');
        this.mask = new Uint8Array(n);
        this.style = new Uint8Array(n);
        this.occupied = new Uint8Array(n);
    }
    idx(x, y) {
        return y * this.w + x;
    }
    set(x, y, c, cls) {
        if (x >= this.w || y >= this.h)
            return;
        const i = this.idx(x, y);
        this.ch[i] = c;
        this.cls[i] = cls;
    }
    /**
     * Accumulate direction bits on a free cell.
     *
     * `cls` is the class to claim the cell for; `border` cells are never
     * reclassified, so a connector meeting a box keeps the box's styling.
     */
    addBits(x, y, bits, cls = 'edge') {
        if (x >= this.w || y >= this.h)
            return;
        const i = this.idx(x, y);
        if (this.occupied[i])
            return;
        this.mask[i] |= bits;
        this.style[i] |= this.curStyle;
        if (this.cls[i] !== 'border')
            this.cls[i] = cls;
    }
    /** Stamp a finished sub-canvas (a subgraph frame's contents) at an offset. */
    blit(sub, ox, oy) {
        for (let sy = 0; sy < sub.h; sy++) {
            for (let sx = 0; sx < sub.w; sx++) {
                const x = ox + sx;
                const y = oy + sy;
                if (x >= this.w || y >= this.h)
                    continue;
                const si = sub.idx(sx, sy);
                const di = this.idx(x, y);
                this.ch[di] = sub.ch[si];
                this.cls[di] = sub.cls[si];
                this.style[di] = sub.style[si];
                this.occupied[di] = 1;
            }
        }
    }
    /** Add direction bits even to an occupied cell, so an edge can meet a border. */
    junction(x, y, bits) {
        if (x >= this.w || y >= this.h)
            return;
        const i = this.idx(x, y);
        this.mask[i] |= bits;
        if (this.cls[i] !== 'border')
            this.cls[i] = 'edge';
    }
    segV(x, y0, y1) {
        const a = Math.min(y0, y1);
        const b = Math.max(y0, y1);
        for (let y = a; y <= b; y++) {
            let bits = 0;
            if (y > a)
                bits |= U;
            if (y < b)
                bits |= D;
            this.addBits(x, y, bits);
        }
    }
    segH(y, x0, x1) {
        const a = Math.min(x0, x1);
        const b = Math.max(x0, x1);
        for (let x = a; x <= b; x++) {
            let bits = 0;
            if (x > a)
                bits |= L;
            if (x < b)
                bits |= R;
            this.addBits(x, y, bits);
        }
    }
    /** Resolve accumulated direction bits into glyphs, honouring line style. */
    finalizeMask() {
        for (let i = 0; i < this.ch.length; i++) {
            if (this.mask[i] !== 0 && this.ch[i] === ' ') {
                const c = maskChar(this.mask[i]);
                this.ch[i] =
                    this.style[i] === STY_DOT ? dottedChar(c) : this.style[i] === STY_THICK ? thickChar(c) : c;
            }
        }
    }
    /**
     * Mirror top-to-bottom for `BT`. Rows reorder but within-row text does not,
     * so labels stay readable; box-drawing glyphs flip to match.
     */
    flipVertical() {
        for (let y = 0; y < Math.floor(this.h / 2); y++) {
            const y2 = this.h - 1 - y;
            for (let x = 0; x < this.w; x++) {
                const i = this.idx(x, y);
                const j = this.idx(x, y2);
                [this.ch[i], this.ch[j]] = [this.ch[j], this.ch[i]];
                [this.cls[i], this.cls[j]] = [this.cls[j], this.cls[i]];
            }
        }
        for (let i = 0; i < this.ch.length; i++)
            this.ch[i] = flipGlyphV(this.ch[i]);
    }
    /**
     * Mirror left-to-right for `RL`. Mirroring reverses each row, so after
     * flipping glyphs each text/label run is reversed back to reading order.
     */
    flipHorizontal() {
        for (let y = 0; y < this.h; y++) {
            for (let x = 0; x < Math.floor(this.w / 2); x++) {
                const x2 = this.w - 1 - x;
                const i = this.idx(x, y);
                const j = this.idx(x2, y);
                [this.ch[i], this.ch[j]] = [this.ch[j], this.ch[i]];
                [this.cls[i], this.cls[j]] = [this.cls[j], this.cls[i]];
            }
        }
        for (let i = 0; i < this.ch.length; i++)
            this.ch[i] = flipGlyphH(this.ch[i]);
        for (let y = 0; y < this.h; y++) {
            let x = 0;
            while (x < this.w) {
                const cls = this.cls[this.idx(x, y)];
                if (cls === 'text' || cls === 'edgeLabel') {
                    const start = this.idx(x, y);
                    while (x < this.w && this.cls[this.idx(x, y)] === cls)
                        x++;
                    const end = this.idx(x, y);
                    reverseSlice(this.ch, start, end);
                }
                else {
                    x++;
                }
            }
        }
    }
    /** Group each row into runs of one class, dropping wide-glyph continuations. */
    toLines() {
        const plain = [];
        const styled = [];
        let width = 0;
        for (let y = 0; y < this.h; y++) {
            // A trailing CONT counts as painted: it is the second cell of a wide
            // glyph, so the row really does reach that column.
            let last = 0;
            for (let x = this.w - 1; x >= 0; x--) {
                if (this.ch[this.idx(x, y)] !== ' ') {
                    last = x + 1;
                    break;
                }
            }
            width = Math.max(width, last);
            const spans = [];
            let plainRow = '';
            let run = '';
            let runCls = 'none';
            for (let x = 0; x < last; x++) {
                const i = this.idx(x, y);
                const c = this.ch[i];
                if (c === CONT)
                    continue;
                const cls = this.cls[i];
                plainRow += c;
                if (cls !== runCls && run !== '') {
                    spans.push({ text: run, cls: runCls });
                    run = '';
                }
                runCls = cls;
                run += c;
            }
            if (run !== '')
                spans.push({ text: run, cls: runCls });
            styled.push(spans);
            // Only ASCII spaces, which is all a blank cell ever holds. Trimming `\s`
            // would eat a trailing NBSP that `styled` keeps, desyncing the two.
            plain.push(plainRow.replace(/ +$/, ''));
        }
        let first = 0;
        while (first < plain.length && plain[first] === '')
            first++;
        let end = plain.length;
        while (end > first && plain[end - 1] === '')
            end--;
        return { plain: plain.slice(first, end), styled: styled.slice(first, end), width };
    }
}
function reverseSlice(arr, start, end) {
    for (let i = start, j = end - 1; i < j; i++, j--) {
        ;
        [arr[i], arr[j]] = [arr[j], arr[i]];
    }
}
/**
 * Paint `text` at `x, y`, one grapheme cluster per cell.
 *
 * A wide cluster claims a second cell, marked with `CONT` so the line builder
 * emits one character for it rather than a stray space.
 */
export function drawText(canvas, text, x, y, cls) {
    let cur = x;
    for (const [cluster, cw] of measured(text)) {
        if (cw === 0)
            continue;
        canvas.set(cur, y, cluster, cls);
        for (let k = 1; k < cw; k++)
            canvas.set(cur + k, y, CONT, cls);
        cur += cw;
    }
}
/**
 * Paint `text` at `x, y`, clearing any edge bits underneath first.
 *
 * Used where text sits on top of a drawn line (sequence messages, dividers,
 * compartment rows) and must win over it.
 */
export function drawTextOverEdges(canvas, text, x, y, cls) {
    let cur = x;
    for (const [cluster, cw] of measured(text)) {
        if (cw === 0)
            continue;
        for (let k = 0; k < cw; k++) {
            if (cur + k < canvas.w && y < canvas.h)
                canvas.mask[canvas.idx(cur + k, y)] = 0;
            canvas.set(cur + k, y, k === 0 ? cluster : CONT, cls);
        }
        cur += cw;
    }
}
export function maskChar(mask) {
    switch (mask) {
        case 0:
            return ' ';
        case U:
        case D:
        case U | D:
            return '│';
        case L:
        case R:
        case L | R:
            return '─';
        case D | R:
            return '┌';
        case D | L:
            return '┐';
        case U | R:
            return '└';
        case U | L:
            return '┘';
        case U | D | R:
            return '├';
        case U | D | L:
            return '┤';
        case D | L | R:
            return '┬';
        case U | L | R:
            return '┴';
        default:
            return '┼';
    }
}
const DOTTED = { '─': '╌', '│': '╎' };
const THICK = {
    '─': '━',
    '│': '┃',
    '┌': '┏',
    '┐': '┓',
    '└': '┗',
    '┘': '┛',
    '├': '┣',
    '┤': '┫',
    '┬': '┳',
    '┴': '┻',
    '┼': '╋',
};
const FLIP_V = {
    '┌': '└',
    '└': '┌',
    '┐': '┘',
    '┘': '┐',
    '┏': '┗',
    '┗': '┏',
    '┓': '┛',
    '┛': '┓',
    '╭': '╰',
    '╰': '╭',
    '╮': '╯',
    '╯': '╮',
    '┬': '┴',
    '┴': '┬',
    '┳': '┻',
    '┻': '┳',
    '▼': '▲',
    '▲': '▼',
    '▽': '△',
    '△': '▽',
};
const FLIP_H = {
    '┌': '┐',
    '┐': '┌',
    '└': '┘',
    '┘': '└',
    '┏': '┓',
    '┓': '┏',
    '┗': '┛',
    '┛': '┗',
    '╭': '╮',
    '╮': '╭',
    '╰': '╯',
    '╯': '╰',
    '├': '┤',
    '┤': '├',
    '┣': '┫',
    '┫': '┣',
    '▶': '◄',
    '◄': '▶',
    '▷': '◁',
    '◁': '▷',
};
export const dottedChar = (c) => DOTTED[c] ?? c;
export const thickChar = (c) => THICK[c] ?? c;
export const flipGlyphV = (c) => FLIP_V[c] ?? c;
export const flipGlyphH = (c) => FLIP_H[c] ?? c;
//# sourceMappingURL=canvas.js.map