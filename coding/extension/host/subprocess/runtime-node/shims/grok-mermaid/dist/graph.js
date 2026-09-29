/**
 * The shared diagram model. Flowchart, state, class and ER sources all parse
 * into a `Graph`; only sequence diagrams have their own model.
 */
import { asciiUpper } from './labels.js';
/** Caps that keep layout bounded; exceeding one drops the diagram to fallback. */
export const MAX_NODES = 128;
export const MAX_EDGES = 512;
export const MAX_GROUPS = 24;
export const MAX_GROUP_DEPTH = 6;
/** Class members / ER attributes listed per box before eliding with `…`. */
export const MAX_MEMBERS = 8;
export const emptyClassInfo = () => ({ annotation: null, attrs: [], methods: [] });
/** `LR`/`RL`/`BT` as written in a header or `direction` statement; else `down`. */
export function parseDir(token) {
    switch (asciiUpper(token)) {
        case 'LR':
            return 'right';
        case 'RL':
            return 'left';
        case 'BT':
            return 'up';
        default:
            return 'down';
    }
}
export class Graph {
    nodes = [];
    edges = [];
    index = new Map();
    groups = [];
    /** Innermost subgraph each node was declared in, parallel to `nodes`. */
    nodeGroup = [];
    curGroup = null;
    /** Set when a cap was hit; the caller abandons the parse. */
    overCap = false;
    /**
     * Text the flowchart grammar could not read and silently discarded.
     *
     * Flowchart parsing is deliberately lenient — a malformed statement
     * contributes whatever prefix parsed and the rest is dropped — so without
     * these the reader gets a clean diagram that is not what they wrote.
     */
    warnings = [];
    dir = 'down';
    constructor(dir = 'down') {
        this.dir = dir;
    }
    /**
     * Index of `id`, creating the node if new. A later declaration carrying a
     * label overwrites the placeholder one an edge created. Returns `null` once
     * `MAX_NODES` is reached, which aborts the parse.
     */
    nodeIndex(id, label, shape) {
        const existing = this.index.get(id);
        if (existing !== undefined) {
            if (label !== null) {
                this.nodes[existing].label = label;
                this.nodes[existing].shape = shape;
            }
            return existing;
        }
        if (this.nodes.length >= MAX_NODES) {
            this.overCap = true;
            return null;
        }
        this.index.set(id, this.nodes.length);
        this.nodes.push({ label: label ?? id, shape });
        this.nodeGroup.push(this.curGroup);
        return this.nodes.length - 1;
    }
    /** Set a node's label without disturbing its shape, creating it if new. */
    nodeLabel(id, label) {
        const existing = this.index.get(id);
        if (existing !== undefined) {
            this.nodes[existing].label = label;
            return existing;
        }
        return this.nodeIndex(id, label, 'round');
    }
    /** Append an edge, or flag `overCap` when `MAX_EDGES` is reached. */
    pushEdge(edge) {
        if (this.edges.length >= MAX_EDGES) {
            this.overCap = true;
            return false;
        }
        this.edges.push(edge);
        return true;
    }
}
//# sourceMappingURL=graph.js.map