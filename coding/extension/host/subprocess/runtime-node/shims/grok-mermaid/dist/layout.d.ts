/**
 * Graph layout: rank, order, place, route, draw.
 *
 * Follows the Sugiyama outline — assign ranks along the flow axis, reorder
 * within ranks to cut crossings, then relax positions on the cross axis so
 * chains stay straight. Edges between adjacent ranks share horizontal "bus"
 * rows; everything else is routed around the diagram through vertical "lanes".
 *
 * `BT` and `RL` reuse the `TD`/`LR` layouts and flip the finished canvas, so
 * text never ends up mirrored.
 */
import { Canvas } from './canvas.ts';
import type { ClassInfo, Edge, Shape } from './graph.ts';
import { Graph } from './graph.ts';
/** A laid-out canvas, or `null` when the diagram is empty or over the cell cap. */
export type CanvasResult = Canvas | null;
export interface Placed {
    x: number;
    y: number;
    w: number;
    h: number;
    cx: number;
    cy: number;
    rank: number;
}
/** What to draw inside a node box. */
export type NodeExtra = {
    kind: 'plain';
} | {
    kind: 'frame';
    sub: Canvas;
} | {
    kind: 'compartments';
    sections: string[][];
};
/**
 * Longest-path ranking over the graph's DAG.
 *
 * Back edges (those closing a cycle) are excluded by a DFS colouring pass, so
 * `A --> B --> C --> A` still ranks 0, 1, 2 rather than diverging.
 */
export declare function computeRanks(graph: Graph): number[];
/**
 * Reorder nodes within each rank to minimise edge crossings (barycenter
 * sweeps): alternate down/up passes sort each rank by the mean position of its
 * neighbours, keeping whichever ordering crossed least.
 */
export declare function orderRanks(byRank: number[][], edges: Edge[], ranks: number[]): void;
export declare function countCrossings(edges: Edge[], ranks: number[], pos: number[]): number;
/**
 * Assign a cross-axis centre to every node so nodes line up under their
 * neighbours: each node drifts toward the average of its neighbours while
 * ranks keep their order and boxes keep `sep` between them.
 */
export declare function assignPositions(byRank: number[][], size: number[], sep: number, edges: Edge[], ranks: number[]): number[];
/** A span competing for a track: `[start, end, from, to, edgeIndex]`. */
type Span5 = [number, number, number, number, number];
/**
 * Pack spans into as few parallel tracks as possible.
 *
 * Two spans share a track when they are two cells apart, or when they share an
 * endpoint — edges fanning out of one node deliberately reuse a single row so
 * a merge draws one arrowhead rather than a stack of them.
 */
export declare function assignTracks(spans: Span5[]): {
    assigned: [number, number][];
    count: number;
};
/** Rank, place, draw and route a graph onto a fresh canvas. */
export declare function layoutCanvas(graph: Graph, extras: NodeExtra[]): CanvasResult;
/** Apply the direction flip a finished canvas needs for `BT` / `RL`. */
export declare function orient(canvas: Canvas, graph: Graph): Canvas;
/** Flowchart and state diagrams: plain boxes, no extra content. */
export declare function layoutFlowchart(graph: Graph): CanvasResult;
/** Class and ER diagrams: boxes divided into title / attribute / method rows. */
export declare function layoutClass(graph: Graph, infos: ClassInfo[]): CanvasResult;
/**
 * Lay out a flowchart that uses `subgraph`.
 *
 * Each subgraph becomes a framed box holding its own independently laid-out
 * canvas. An edge is drawn in the innermost scope containing both endpoints;
 * one crossing a subgraph boundary attaches to the frame instead of the node.
 */
export declare function layoutGrouped(graph: Graph): CanvasResult;
export declare function drawBox(canvas: Canvas, p: Placed, lines: string[], shape: Shape): void;
export {};
//# sourceMappingURL=layout.d.ts.map