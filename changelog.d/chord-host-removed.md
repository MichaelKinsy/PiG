### Removed

- The Go package `chord/host` (Chord v1: `CreateHost`, `Cell`, `Derived`, the v1 contracts, links, host manager and Node facet loader) is gone, as Pi removed Chord v1 when Chord v2 became its root export. Use `chord/next/{contracts,data,state,registries,link,host,store}` and `chord/nextnode`. The root package `chord` no longer has replicated state (`NewReplicatedState`, `AttachReplicatedStateSource`, `ReplicatedState` and its delivery types); it keeps `AggregateError`, `SetUncaughtErrorReporter`, strict JSON and watches.
