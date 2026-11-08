/**
 * `classDiagram`: classes with member compartments and UML relations.
 * Lenient: an unreadable statement is dropped and recorded in `warnings`.
 *
 * Compartments live on `Node.sections`: `[title, attrs, methods]`, where the
 * title is the optional `«annotation»` line followed by the class name.
 */
import { Graph, type Node } from '../graph.ts';
import type { Diagram } from '../registry.ts';
export declare const classDiagram: Diagram;
export declare function parseClass(src: string): Graph | null;
/** Add a member to the attribute or method compartment, eliding past the cap. */
export declare function pushMember(node: Node, raw: string): void;
//# sourceMappingURL=class.d.ts.map