// The Context of the Session commit whose publication the host is delivering (session.ts: a publication carries its commit's Context,
// and observers' deliveries take its values). The core's `published` notice names no commit, but commits hold the Session line one at
// a time and a commit's publication arrives before its answer settles, so the commit that holds the line is the producer.
import type { Context } from "@earendil-works/chord"
import type { CoreClient } from "../client.ts"

const holding = new WeakMap<CoreClient, Context[]>()

/** The commit with this Context holds the line; the returned function ends that. */
export function enterCommit(client: CoreClient, context: Context): () => void {
  const stack = holding.get(client) ?? holding.set(client, []).get(client)!
  stack.push(context)
  return () => { const at = stack.lastIndexOf(context); if (at >= 0) stack.splice(at, 1) }
}

/** The Context of the commit that holds the line, if one does. */
export const currentCommit = (client: CoreClient): Context | undefined => holding.get(client)?.at(-1)
