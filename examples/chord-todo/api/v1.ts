// The todo app's API, version 1: the module its plugins import as `@todo/app/v1`. It re-exports the part of Chord the app
// supports; a plugin never imports Chord itself.
export * from "@earendil-works/chord";
export { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";

import { defineFacet as chordFacet, type FacetHost, type FacetSelf, hostKind, str } from "@earendil-works/chord";
import type { TodoPrincipal } from "./principals.ts";

export type { TodoPrincipal } from "./principals.ts";

/** Defines a facet of the todo app: its factories get the app's principals. */
export function defineFacet(setup: (host: FacetHost<TodoPrincipal>, self: FacetSelf) => void) {
	return chordFacet<TodoPrincipal>(setup);
}

/** A todo list: a worker process of its own, with its own store. */
export const List = hostKind("list", { id: str() });
