# chord

Go port of Pi's `@earendil-works/chord`: the root package (strict JSON, the shared Context, replicated state, watches) with `chord/context`, `chord/delta`, and the Chord v2 packages under `chord/next` and `chord/nextnode`.

Chord v2's design documents are Pi's `packages/chord/docs/next` (decisions, layers, slices, store, flow control); they are not ported: the Go packages `chord/next/{contracts,data,state,registries}` mirror the source layout those documents describe.
