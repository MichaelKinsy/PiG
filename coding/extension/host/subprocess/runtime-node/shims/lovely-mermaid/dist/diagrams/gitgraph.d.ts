/**
 * `gitGraph`: commit lanes, drawn the way `git log --graph` draws them —
 * newest commit on top, one column per branch, connector rows where history
 * splits. Lenient: an unreadable statement is dropped and recorded in
 * `warnings`.
 *
 * Connector rows go through the canvas direction bits, so a merge reaching
 * across an active lane crosses it with a `┼` instead of erasing it.
 */
import type { Diagram } from '../registry.ts';
export declare const gitgraph: Diagram;
//# sourceMappingURL=gitgraph.d.ts.map