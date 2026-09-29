/**
 * Sequence diagram layout.
 *
 * Participants get one column each, with lifelines running the full height and
 * a box repeated at top and bottom. Column gaps are solved from the widest
 * thing that has to fit between any two columns — a message label, a note, a
 * self-message stub — then items stack down the canvas in source order.
 */
import { type CanvasResult } from './layout.ts';
import type { Sequence } from './parse.ts';
export declare function layoutSequence(seq: Sequence): CanvasResult;
//# sourceMappingURL=layout-seq.d.ts.map