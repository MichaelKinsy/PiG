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
    /**
     * Set when a size cap was hit, naming the cap. The parser stops consuming
     * statements and renders the prefix, warning about the truncation — a
     * streamed diagram that outgrows a cap can never shrink back under it, so
     * a stable truncated render beats flipping to the source box for good.
     */
    truncated = null;
    /**
     * Source the grammar could not read and dropped. Parsing is lenient in
     * every grammar: a statement either contributes what parsed or is dropped
     * and recorded here, so the reader can tell a clean diagram from one that
     * is missing something they wrote.
     */
    warnings = [];
    /** Parsed `classDef` declarations: name -> property map. */
    classDefs = {};
    dir = 'down';
    constructor(dir = 'down') {
        this.dir = dir;
    }
    /**
     * Index of `id`, creating the node if new. A later declaration carrying a
     * label overwrites the placeholder one an edge created. Returns `null` once
     * `MAX_NODES` is reached, which truncates the parse.
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
            this.truncated ??= `node cap (${MAX_NODES}) reached`;
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
    /** Attach an author class name to a node, ignoring a repeat. */
    addClass(idx, name) {
        const node = this.nodes[idx];
        node.classes ??= [];
        if (!node.classes.includes(name))
            node.classes.push(name);
    }
    /**
     * Apply collected `[ids, names]` class assignments. Run after the statement
     * walk so a `class A,B name` (or `:::` tag) may precede the nodes it names;
     * unknown ids are ignored.
     */
    applyClasses(assignments) {
        for (const [ids, names] of assignments) {
            for (const id of ids) {
                const idx = this.index.get(id.trim());
                if (idx === undefined)
                    continue;
                for (const name of names) {
                    if (name.trim() !== '')
                        this.addClass(idx, name.trim());
                }
            }
        }
    }
    /** Apply `[id, url]` link targets; the last one per id wins, unknown ids
     * are ignored. Deferred like `applyClasses`, for the same ordering reason. */
    applyHrefs(hrefs) {
        for (const [id, url] of hrefs) {
            const idx = this.index.get(id.trim());
            if (idx !== undefined)
                this.nodes[idx].href = url;
        }
    }
    /**
     * Record an unreadable statement; the diagram renders without it. Skipped
     * once truncated — a cap-caused parse failure is not the statement's fault.
     */
    drop(st) {
        if (this.truncated === null)
            this.warnings.push(`dropped, unreadable statement: "${st}"`);
    }
    /** Append an edge, or flag `truncated` when `MAX_EDGES` is reached. */
    pushEdge(edge) {
        if (this.edges.length >= MAX_EDGES) {
            this.truncated ??= `edge cap (${MAX_EDGES}) reached`;
            return false;
        }
        this.edges.push(edge);
        return true;
    }
}
//# sourceMappingURL=graph.js.map