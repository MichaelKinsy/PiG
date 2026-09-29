# Experimental server, worker and client

This package ports Pi's `src/experimental` application: the coordinator, server, Session worker, client runtime, client TUI and command parser. Only the unshipped `pig_experimental` build of `cmd/pig` (see `cmd/pig-experimental/README.md`) dispatches it. The stable CLI never does.

Experimental Radius composition is designed out (D64). The server and client use native Unix sockets only. The explicit-gateway Radius relay library from `experimental/radius-auth.ts` and `experimental/radius-relay.ts` remains in this package with its ported tests; no application path selects it.

On Windows, `EnsurePrivateServerDirectory` and the client Unix transport reject startup as Pi does (no POSIX user ID; `Unix transport is not supported on Windows`). The coordinator itself uses AF_UNIX socket files on every platform.

## Radius relay library

### Selection and authentication

Supply the gateway explicitly to `NewRadiusRelayAuthResolver`.
Supply either a token, a token file, or a lazy `CreateRuntime` callback.
Configure that runtime without model-network refresh and with OAuth refresh directed to the selected gateway.
Do not use the built-in Earendil Radius runtime as a substitute for a PiG-owned gateway (D64).
The existing `RequestAuthRuntime` supports an explicit `radius` provider configuration with `oauth: "radius"` and the selected `baseUrl`.
`TestRadiusAuthRefreshesFiveMinuteCredentialAtLocalGateway` exercises that composition against a local gateway.

The resolver checks context cancellation before `PI_OFFLINE` presence.
Offline mode performs no token-file or runtime access.
The resolver trims explicit tokens using JavaScript whitespace semantics and reads a token file anew for every attempt.
The resolver creates a stored-auth runtime once and resolves credentials anew for every attempt.
Stored OAuth requires five minutes of remaining validity, including after refresh.

### Ownership and async contract

`RadiusRelayHost.Start` owns one reconnect loop.
`RadiusRelayHost.Close` cancels and joins opening, serving, pending writes, and retry waits.
The host accepts independent virtual connections in arrival order.
Pong and unknown or rejected connection-close replies reserve their order and bytes immediately without waiting for socket writes.
One owned writer drains control and data submissions in that order while the reader continues delivering inbound data, closes, and opens.
Control-write failures close the socket with the transport-error code and report the failure on the reader.
Shutdown rejects queued writes and joins the active write and all completion callbacks.
A local virtual close writes its non-nil final chunk before its close control.
A remote close or host failure reports one terminal callback per active connection in insertion order.

`CreateRadiusClientTransportFactory` awaits authentication and the WebSocket handshake.
The returned transport delivers raw binary messages without the host multiplexing envelope.
`Send` copies each submission and awaits its ordered write.
Pending submissions share the upstream four-frame byte budget.
Native socket writes provide backpressure instead of browser `bufferedAmount` polling.
`Close` initiates local shutdown without synthesizing a remote callback.
Wait on `Done` to join transport cleanup.

Callbacks execute on the transport reader, not the TUI loop.
Do not wait for that reader's shutdown from its own callback.
Marshal any UI mutation through the UI owner's executor.

`RadiusClientReconnect` observes an established client.
It retries after one second, doubles failed-attempt delays to thirty seconds, and restores the last selected Session after reconnect succeeds.
A disconnected attachment reset preserves the selection.
An explicit detach while connected clears it.
A failed reattachment disconnects the client and retries.
`Dispose` removes listeners, cancels pending work, disconnects, and joins the retry loop.

## Server selection

`ResolveServerDirectory` resolves an explicit value, then the selected environment override, then the server directory beside the agent tree. Default mode uses `PIG_SERVER_DIR` and the PiG configuration root's `server` directory. `PIG_USE_PI_DIRS=1` uses `PI_SERVER_DIR` and `~/.pi/server` instead (D2). The logical ID follows `PIG_SERVER_ID` or `PI_SERVER_ID` in the same selected namespace. Explicit empty values are retained; directory resolution and existing profile validation own their meaning.

`RunServerProcess` validates internal arguments and model JSON before startup, selects automatic lifetime policy, and waits for server closure. It registers one handler per termination signal after startup and joins shutdown before returning. The executable owns internal-role validation and consumption; this function does not consume the role marker. Parser failures preserve the fixed diagnostic and their syntax cause separately.

## Plugin package selections

`RestoreServerPluginPackageProfile` persists normalized package selections for later server generations. A nil selection restores the saved value. An explicit empty selection removes the server profile. Session profiles distinguish an absent selection from an explicitly empty selection and bind the stored paths to the exact Session path. Profile files use Pi's existing version-1 JSON shape and mode 0600. These APIs only manage selections; they do not build or load JavaScript facet bundles or activate experimental command routing.

## Automatic native activation

`StartServer` composes the native coordinator, worker manager, JSONL-backed service catalog, plugin selection/builds and Unix backend. `RunningServer.Closed` observes backend/catalog closure; `Close` also joins worker and coordinator cleanup. `StartForegroundServer` serializes that startup with automatic activation. No relay is opened (D64).

`ActivateServer` acquires the server profile and activation lock, connects to an existing endpoint, or spawns the internal server role and waits for its native handshake. Failed activation terminates and joins its child before releasing the lock. Worker manager/process APIs preserve model-option presence, generation demand, operation ownership and joined shutdown. `RunSessionWorkerProcess` runs the coding-Harness factory. The worker closes its resources on retirement, shutdown, disconnection, SIGTERM or SIGINT, and leaves its control socket to process exit, so the coordinator reports the disconnection only when the worker is exiting.

## Command parsing

`Cli.Parse` parses the experimental server/client command group without reading configuration, opening a connection, or dispatching a process. `Cli.Execute` awaits the selected `CliContext` callback and returns its error on the same call. Unknown options stop the prefix parser and remain available to the command builder. Radius addresses and relay credential options are unsupported (D64).

## Client application composition

`OpenClientRuntime` validates selection, discovers or activates local routes, and owns the real client and server/Session namespaces. Non-Unix routes are rejected before discovery (D64). `Dispose` marks the runtime disposed before joining source and client cleanup phases. A repeated or reentrant call returns immediately, matching Pi's disposed flag.

`RunClient` lists, creates or attaches Sessions through those services. One-shot prompts wait for both the operation response and its independent terminal transcript event. `OnEvent` receives the exact strict-JSON `LaneWatchEvent` payload in order; callbacks are awaited and their errors propagate. The broader in-process `HarnessEvent` is not substituted for that wire representation. Result variants are values: `ClientListResult`, `ClientAttachedResult` and `ClientPromptedResult`, with `Kind()` and the corresponding JSON fields.

## Client service namespaces

`NewClientServerServiceSource` and `NewClientSessionServiceSource` adapt the real framed Client to the same structural Chord source contracts used by loopback presentations. An optional `TransportClient` decorator changes request/subscription behavior without fabricating connection or attachment state. Bindings use `BeginReady` and `BeginRebind` to preserve the synchronous admission/fencing prefix; each source owns and drains its continuation work.

`ActivateBuiltinClientServices` acquires Session directory/management/plugins and Models/controller/Transcript facades and waits for both namespaces. Management attach/detach/remove operations wait for the matching attachment hydration transition before returning. These APIs do not open or discover servers by themselves.

A selected `AgentController` view also exposes `AgentControllerInitiator`. Its `Begin*` methods resolve the current facet override and return only after that implementation admits the operation. Typed operations expose `Wait(waitContext)`, with the same result decoding as an awaited call. Cancelling this observer does not replace or cancel the operation's original context. The nine upstream wire methods remain unchanged. An in-host provision reaches the view through the host's loopback binding; the provider forwards admission to an implementation that implements `chord.ServiceMemberInitiator`. A blocking local override without that boundary reports `chord.ErrInvocationAdmissionUnavailable`; the client TUI then awaits its blocking method as Pi awaits the returned Promise.

## Server-owned facet builds

`BundleFacets` builds caller-selected opaque entries into a caller-selected output directory. `Entries` is an ordered `[]FacetEntrySource`, not a Go map, because upstream record enumeration determines validation order. Repeated names retain their first position and last value. Integer property names enumerate first. Compilation then uses upstream's locale-sorted entry order. An omitted plugin version stays omitted. Direct builds default to no source maps.

`BundleFacetPackage` applies package metadata and caller-supplied `DefaultFacets` before invoking the same compiler. It returns the manifest paths and canonical package directory/package.json paths. It does not inject session or TUI conventions. Package source maps default to true and can be disabled in `chord.sourceMap`. The direct options currently expose the node22.19 default compiler configuration; optional minify, define, platform and target selection are not implemented.

`CreateServerPluginPackage` returns a serialized builder with `ManifestPath` and `Build(ctx)`. It reads package metadata, applies the conventional `src/session.ts` and `src/tui.ts` entries, validates lexical and canonical package containment, and preserves configured overrides/disables. Builds use esbuild 0.28.2, the same Go compiler behind the pinned Pi JavaScript API. They emit content-addressed CommonJS entries, SHA-256 integrity, declared external imports, and source maps selected by package metadata. A successful build replaces the server-owned cache directory transactionally. The result contains the transportable TUI artifact, or an empty list when the package has no TUI entry.

Build waits for admitted compiler and filesystem work; Pi's builder has no AbortSignal. It does not evaluate JavaScript. Facet execution belongs to the separately selected Node extension host. These APIs do not enable experimental routing in the stable CLI.

## Evidence

Run `go test -race -count=3 ./internal/experimental`.
The suite includes the relevant pinned upstream relay cases, malformed controls and envelopes, token rotation, local OAuth refresh, a native WebSocket echo gateway, ordered bidirectional backpressure, reconnect selection, and cancellation/cleanup tests.
`TestPinnedUpstreamRadiusSource` executes the actual pinned TypeScript modules with local dependency adapters and rejects implicit endpoints or live sockets.
The compiler adapter uses the repository's existing TypeScript installation and installs nothing.

Run `go test ./internal/experimental -run '^$' -bench 'Benchmark(RelayDataFrame|RadiusClientSend|RadiusHostControl)$' -benchmem` to measure framing, client-send, and host-control allocation costs.
These benchmarks do not claim a speedup or complete CLI/server parity.
The server's accept adapter and the established client's reconnect adapter remain caller-owned library boundaries.
