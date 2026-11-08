/**
 * The diagram registry: one entry per supported diagram type.
 *
 * `diagramKind` and `render` both resolve through this table, so the header
 * test each parser gates on and the one `diagramKind` reports are the same
 * function by construction. Adding a diagram type is one module in
 * `diagrams/` plus one entry here.
 */
import { classDiagram } from './diagrams/class.js';
import { er } from './diagrams/er.js';
import { flowchart } from './diagrams/flowchart.js';
import { gitgraph } from './diagrams/gitgraph.js';
import { mindmap } from './diagrams/mindmap.js';
import { pie } from './diagrams/pie.js';
import { sequence } from './diagrams/sequence.js';
import { state } from './diagrams/state.js';
import { timeline } from './diagrams/timeline.js';
import { stripControls } from './labels.js';
import { headerKind, statementsOf } from './statements.js';
export const DIAGRAMS = [
    flowchart,
    state,
    classDiagram,
    er,
    sequence,
    pie,
    mindmap,
    timeline,
    gitgraph,
];
/** The registry entry `src`'s header declares, or `null`. */
export function diagramFor(src) {
    const header = headerKind(statementsOf(src));
    if (header === null)
        return null;
    return DIAGRAMS.find((d) => d.headers.includes(header)) ?? null;
}
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
export function diagramKind(src) {
    // The same strip `render` applies, so the two entry points agree on any src.
    return diagramFor(stripControls(src))?.kind ?? null;
}
//# sourceMappingURL=registry.js.map