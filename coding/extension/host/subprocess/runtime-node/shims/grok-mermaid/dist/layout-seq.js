/**
 * Sequence diagram layout.
 *
 * Participants get one column each, with lifelines running the full height and
 * a box repeated at top and bottom. Column gaps are solved from the widest
 * thing that has to fit between any two columns — a message label, a note, a
 * self-message stub — then items stack down the canvas in source order.
 */
import { Canvas, D, drawTextOverEdges, L, R, U } from './canvas.js';
import { fitLabel, WRAP_WIDTH } from './labels.js';
import { drawBox } from './layout.js';
import { stringWidth } from './width.js';
const PAD = 1;
/** Minimum columns between adjacent lifelines. */
const SEQ_GAP = 5;
const MAX_CANVAS_CELLS = 1 << 21;
const sat = (a, b) => Math.max(0, a - b);
const half = (n) => Math.floor(n / 2);
/** Where a note box sits, given the lifeline positions. */
function noteGeometry(xs, anchor, textW) {
    if (anchor.kind === 'over') {
        const center = half(xs[anchor.from] + xs[anchor.to]);
        const w = Math.max(xs[anchor.to] - xs[anchor.from] + 5, textW + 2 * PAD + 2);
        return { x: sat(center, half(w)), w };
    }
    const w = textW + 2 * PAD + 2;
    if (anchor.kind === 'left')
        return { x: sat(xs[anchor.at], 2 + w - 1), w };
    return { x: xs[anchor.at] + 2, w };
}
const itemTextW = (text) => (text === null ? 0 : stringWidth(text));
export function layoutSequence(seq) {
    const n = seq.labels.length;
    const labels = seq.labels.map((l) => fitLabel(l, WRAP_WIDTH));
    const boxW = labels.map((l) => Math.max(1, stringWidth(l)) + 2 * PAD + 2);
    const boxH = 3;
    const gaps = Array.from({ length: sat(n, 1) }, (_, i) => Math.max(SEQ_GAP, Math.ceil(boxW[i] / 2) + Math.ceil(boxW[i + 1] / 2) + 1));
    // Each requirement is "columns l..r together need at least `need` cells".
    const reqs = [];
    for (const item of seq.items) {
        if (item.kind === 'message') {
            const tw = itemTextW(item.text);
            if (item.from !== item.to) {
                reqs.push([Math.min(item.from, item.to), Math.max(item.from, item.to), Math.max(tw + 2, 4)]);
            }
            else if (item.from + 1 < n) {
                reqs.push([item.from, item.from + 1, 5 + tw + 2]);
            }
        }
        else if (item.kind === 'note') {
            const tw = stringWidth(item.text);
            const a = item.anchor;
            if (a.kind === 'over' && a.from < a.to) {
                reqs.push([a.from, a.to, sat(tw, 1)]);
            }
            else if (a.kind === 'over') {
                const need = Math.ceil((tw + 4) / 2) + 2;
                if (a.from > 0)
                    reqs.push([a.from - 1, a.from, need]);
                if (a.from + 1 < n)
                    reqs.push([a.from, a.from + 1, need]);
            }
            else if (a.kind === 'left' && a.at > 0) {
                reqs.push([a.at - 1, a.at, tw + 7]);
            }
            else if (a.kind === 'right' && a.at + 1 < n) {
                reqs.push([a.at, a.at + 1, tw + 7]);
            }
        }
    }
    // Narrowest spans first, so a wide requirement absorbs what they already gave.
    reqs.sort((a, b) => a[1] - a[0] - (b[1] - b[0]));
    for (const [l, r, need] of reqs) {
        let cur = 0;
        for (let i = l; i < r; i++)
            cur += gaps[i];
        if (cur < need)
            gaps[r - 1] += need - cur;
    }
    const xs = new Array(n);
    xs[0] = half(boxW[0]);
    for (let i = 1; i < n; i++)
        xs[i] = xs[i - 1] + gaps[i - 1];
    let canvasW = xs[n - 1] + Math.ceil(boxW[n - 1] / 2) + 1;
    for (const item of seq.items) {
        if (item.kind === 'message' && item.from === item.to) {
            canvasW = Math.max(canvasW, xs[item.from] + 5 + itemTextW(item.text) + 1);
        }
        else if (item.kind === 'note') {
            const g = noteGeometry(xs, item.anchor, stringWidth(item.text));
            canvasW = Math.max(canvasW, g.x + g.w + 1);
        }
        else if (item.kind === 'divider') {
            canvasW = Math.max(canvasW, stringWidth(item.text) + 4);
        }
    }
    const rows = [];
    let y = boxH + 1;
    for (const item of seq.items) {
        rows.push(y);
        y += rowHeight(item);
    }
    const bottomTop = y;
    const canvasH = bottomTop + boxH;
    if (canvasW * canvasH > MAX_CANVAS_CELLS)
        return null;
    const canvas = new Canvas(canvasW, canvasH);
    for (let i = 0; i < n; i++) {
        for (const by of [0, bottomTop]) {
            drawBox(canvas, box(sat(xs[i], half(boxW[i])), by, boxW[i], boxH), [labels[i]], 'rect');
        }
    }
    seq.items.forEach((item, k) => {
        if (item.kind !== 'note')
            return;
        const g = noteGeometry(xs, item.anchor, stringWidth(item.text));
        drawBox(canvas, box(g.x, rows[k], g.w, 3), [item.text], 'rect');
    });
    for (const x of xs) {
        canvas.junction(x, boxH - 1, D);
        canvas.segV(x, boxH, bottomTop - 1);
        canvas.junction(x, bottomTop, U);
    }
    seq.items.forEach((item, k) => {
        const r = rows[k];
        if (item.kind === 'message')
            drawMessage(canvas, item, xs, r);
        else if (item.kind === 'divider')
            drawDivider(canvas, item.text, r, canvasW);
    });
    canvas.finalizeMask();
    return canvas;
}
function rowHeight(item) {
    if (item.kind === 'note')
        return 4;
    if (item.kind === 'divider')
        return 2;
    if (item.from === item.to)
        return 4;
    return item.text !== null ? 3 : 2;
}
/** Geometry for a box drawn by position and size; ranks are irrelevant here. */
const box = (x, y, w, h) => ({
    x,
    y,
    w,
    h,
    cx: x + half(w),
    cy: y + 1,
    rank: 0,
});
function drawMessage(canvas, item, xs, r) {
    const lineCh = item.dashed ? '╌' : '─';
    if (item.from === item.to) {
        // A stub that leaves the lifeline and returns two rows down.
        const x = xs[item.from];
        canvas.junction(x, r, R);
        canvas.set(x + 1, r, lineCh, 'edge');
        canvas.set(x + 2, r, lineCh, 'edge');
        canvas.set(x + 3, r, '╮', 'edge');
        canvas.set(x + 3, r + 1, '│', 'edge');
        canvas.set(x + 1, r + 2, item.head === 'cross' ? '×' : '◄', 'edge');
        canvas.set(x + 2, r + 2, lineCh, 'edge');
        canvas.set(x + 3, r + 2, '╯', 'edge');
        if (item.text !== null)
            drawTextOverEdges(canvas, item.text, x + 5, r + 1, 'text');
        return;
    }
    const x0 = xs[item.from];
    const x1 = xs[item.to];
    const rightward = x1 > x0;
    // A labelled message writes its text on `r` and draws the arrow below it.
    const arrowRow = item.text !== null ? r + 1 : r;
    const lo = Math.min(x0, x1);
    const hi = Math.max(x0, x1);
    canvas.junction(x0, arrowRow, rightward ? R : L);
    for (let x = lo + 1; x < hi; x++)
        canvas.set(x, arrowRow, lineCh, 'edge');
    const headCh = item.head === 'cross' ? '×' : rightward ? '▶' : '◄';
    canvas.set(rightward ? x1 - 1 : x1 + 1, arrowRow, headCh, 'edge');
    if (item.text !== null) {
        const span = hi - lo - 1;
        const t = fitLabel(item.text, Math.max(1, span));
        drawTextOverEdges(canvas, t, lo + 1 + half(sat(span, stringWidth(t))), r, 'text');
    }
}
/** A full-width rule labelling a `loop` / `alt` / `opt` block boundary. */
function drawDivider(canvas, text, r, canvasW) {
    for (let x = 0; x < canvasW; x++)
        canvas.set(x, r, '─', 'edge');
    drawTextOverEdges(canvas, ` ${fitLabel(text, sat(canvasW, 4))} `, 2, r, 'edgeLabel');
}
//# sourceMappingURL=layout-seq.js.map