/**
 * `erDiagram`: entities with attribute compartments and crow's-foot
 * relationships. Lenient: an unreadable statement is dropped and recorded in
 * `warnings`.
 *
 * Entities use `Node.sections` as `[title, attrs]`.
 */
import { Graph, type Node } from '../graph.ts';
import type { Diagram } from '../registry.ts';
export declare const er: Diagram;
export declare function parseEr(src: string): Graph | null;
/** ER attributes are `type name`; a trailing quoted comment is dropped. */
export declare function pushErAttribute(node: Node, raw: string): void;
//# sourceMappingURL=er.d.ts.map