# chord

Go port of Pi's `@earendil-works/chord`: the root package (strict JSON, the shared Context, watches) with `chord/context`, `chord/delta`, the Chord v2 packages under `chord/next` and `chord/nextnode`, and `chord/sync` (session sync over a store's outbox: upload, pull, snapshots, folds and the flush scheduler; experimental, Pi's `@earendil-works/chord/sync`).

Chord v2's design documents are Pi's `packages/chord/docs/next` (decisions, layers, slices, store, flow control); they are not ported: the Go packages `chord/next/{contracts,data,state,registries,link,host}` mirror the source layout those documents describe.
