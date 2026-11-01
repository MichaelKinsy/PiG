### Added

- Go API `chord/next/host`: `Host.Reload(facets, ctx, ReloadOptions)`, the reload in the kernel (Pi `4b535d57`, per plugin since `a14cdf90`): the complete new plugin set is staged per plugin against a recording `self`, committed once (types, registries, contributions), and each object takes its fate (a pure swap in place, a native restart after its drain, a retry on the new code, or its end by its type's removal) with a `ReloadReport`. A plugin whose new code fails keeps its old code (an added one is left out), and a plugin whose recorded requests would not bind because of such a plugin keeps its old code too (the closure rule). `Host.Load` now returns a `LoadReport` listing the plugins left out (`PluginFailure`), and never closes the host for a failing plugin. `Host.Route` returns a `RouteHandle` with `Retry` and `Suspend`.
- Go API `chord/nextnode/plugins`: Pi's plugins component (slice 7c): plugin discovery, dependency installs, the watcher's marks (`dirty`, `install`, `set`, `base`), the build (`BuildFacet`, a 1:1 port of `build.ts` on esbuild's Go API at Pi's pin 0.28.2), the plugin runtime (`NewPluginRuntime`, `NodeHost`: Pi's own `evaluate.ts`, `stack.ts` and `runtime.ts` evaluate and load the built scripts in a Node subprocess whose chord host is the child of a Go host over the chord link wire, as a forked worker is of its server: the app attaches `NodeHost.Link()` with `Host.Route` or `Host.Accept`; Node 22.19 or newer), and the component (`CreatePlugins`: per kind current sets, `Build`, `Load`, `Reload`, `Step`, `Rollout`). Pi's chord sources and their two packages are vendored under `chord/nextnode/plugins/node` (`make vendor-chord-plugins-node`, run by `make pin-bump`).
- Go API `chord/next/store`: `ChordStore.CommitSeries`, several commits in one backend transaction (Pi `e5c6182b`), and the table version fence of `TableHandle`.

### Changed

- References are the only indirection (Pi `4b535d57`): a reference's members and keys attach straight to the object instance's own document or derive and move to a new instance with one frame, keeping their last values while the object is unbound; consumers of an older contract version read through the instance's shared mapping (`accepts`, `Provider.Accepts`). The member sources and keyed members of the host are removed.
- A route has no backoff timer (Pi slice 10 R2): a rejected `resolve`, or a link closed before its handshake, fails the address's holders `failed { waiting }` until a new hold, `RouteHandle.Retry` or a link that completed its handshake dropping.
- A `retry` over a link carries a hop budget (`hops`, 16 from the first host, one less per host) instead of an operation id and the objects it passed through (Pi audit A5); `parked_long` is reported for any long park, naming the first blocker and its state; registry cycles are caught when a registry composes, not when a contribution is made.
- `chord/output`: `SanitizeOutput` removes whole ANSI escape sequences, not only their control characters (Pi `50d9f823`, `output.ts`).

### Upgrading

- The `retry` message of the Chord v2 link protocol (`chord/next/link`) has `hops` in place of `operation` and `through`; both ends of a link must be built from the same revision (the protocol is experimental and has no version negotiation).
<!-- feat: chord-v2-reload-7b -->
