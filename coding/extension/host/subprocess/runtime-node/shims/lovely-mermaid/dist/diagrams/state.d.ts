/**
 * `stateDiagram` / `stateDiagram-v2`: states, transitions, descriptions and
 * composite states. Lenient: an unreadable statement is dropped and recorded
 * in `warnings`.
 *
 * A composite (`state X { ... }`) becomes a `Group`, drawn as a titled frame
 * by the same machinery flowchart subgraphs use; `--` splits a composite into
 * unlabelled sibling region groups. `[*]` is scoped per group, so an inner
 * start dot is a different node from the outer one.
 */
import { Graph } from '../graph.ts';
import type { Diagram } from '../registry.ts';
export declare const state: Diagram;
export declare function parseState(src: string): Graph | null;
//# sourceMappingURL=state.d.ts.map