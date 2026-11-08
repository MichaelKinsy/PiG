/**
 * `graph` / `flowchart`: node chains with inline shapes and labelled links,
 * plus `subgraph` grouping.
 *
 * The grammar is lenient, inherited from upstream and mermaid.js itself:
 * a statement contributes whatever prefix parsed and the rest is dropped,
 * recorded in `graph.warnings`.
 */
import { Graph } from '../graph.ts';
import type { Diagram } from '../registry.ts';
export declare const flowchart: Diagram;
export declare function parseGraph(src: string): Graph | null;
//# sourceMappingURL=flowchart.d.ts.map