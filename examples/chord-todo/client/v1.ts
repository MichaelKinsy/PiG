// The todo app's API v1 as the page and the terminal have it (`@todo/app/v1` in their base bundles): the same names as `api/v1.ts`,
// from the Chord the client tree vendors. A base bundle is built from files by path, so it names Chord's files, where the plugins'
// host resolves `@earendil-works/chord` itself.

import { defineFacet as chordFacet, type FacetHost, type FacetSelf, hostKind, str } from "../../../chord/nextnode/surfaces/client/pi/chord/src/index.ts";
import type { TodoPrincipal } from "../api/principals.ts";

export * from "../../../chord/nextnode/surfaces/client/pi/chord/src/index.ts";
export { BACKGROUND_CONTEXT } from "../../../chord/nextnode/surfaces/client/pi/chord/src/context/index.ts";
export type { TodoPrincipal } from "../api/principals.ts";

/** Defines a facet of the todo app: its factories get the app's principals. */
export function defineFacet(setup: (host: FacetHost<TodoPrincipal>, self: FacetSelf) => void) {
	return chordFacet<TodoPrincipal>(setup);
}

/** A todo list: a worker process of its own, with its own store. */
export const List = hostKind("list", { id: str() });
